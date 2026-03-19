#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TF_DIR="${REPO_ROOT}/infra/digitalocean"

ARTILLERY_CONFIG="${REPO_ROOT}/artillery/artillery.yaml"
RESULTS_ROOT="${REPO_ROOT}/benchmark-results"
NAME_PREFIX="${NAME_PREFIX:-go-raft-bench}"
DO_REGION="${DO_REGION:-nyc3}"
DO_IMAGE="${DO_IMAGE:-ubuntu-24-04-x64}"
RAFT_NODE_COUNT="${RAFT_NODE_COUNT:-3}"
RAFT_PORT="${RAFT_PORT:-8081}"
RAFT_DROPLET_SIZE="${RAFT_DROPLET_SIZE:-s-1vcpu-1gb}"
ARTILLERY_RUNNER_SIZE="${ARTILLERY_RUNNER_SIZE:-s-1vcpu-1gb}"
OPERATOR_CIDR="${OPERATOR_CIDR:-}"
SSH_USER="${SSH_USER:-root}"
SSH_OPTS=(
  -o BatchMode=yes
  -o ConnectTimeout=10
  -o StrictHostKeyChecking=accept-new
  -o UserKnownHostsFile="${HOME}/.ssh/known_hosts"
)

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR=""
RUNNER_PUBLIC_IP=""
RUNNER_PRIVATE_IP=""
ARTILLERY_TARGET_URL=""
TERRAFORM_READY=0
ARTIFACTS_FETCHED=0
declare -a RAFT_PUBLIC_IPS=()
declare -a RAFT_PRIVATE_IPS=()

usage() {
  cat <<EOF
Usage: $(basename "$0") [-c ARTILLERY_CONFIG] [-r RESULTS_ROOT]

Environment:
  DO_TOKEN                   DigitalOcean API token (required)
  DO_SSH_KEY_FINGERPRINT     Existing DigitalOcean SSH key fingerprint or ID (required)
  OPERATOR_CIDR              CIDR allowed to SSH and hit raft HTTP; auto-detected if omitted
  NAME_PREFIX                Resource name prefix (default: ${NAME_PREFIX})
  DO_REGION                  DigitalOcean region (default: ${DO_REGION})
  DO_IMAGE                   Droplet image slug (default: ${DO_IMAGE})
  RAFT_NODE_COUNT            Number of raft nodes (default: ${RAFT_NODE_COUNT})
  RAFT_PORT                  Raft HTTP port (default: ${RAFT_PORT})
  RAFT_DROPLET_SIZE          Raft node size slug (default: ${RAFT_DROPLET_SIZE})
  ARTILLERY_RUNNER_SIZE      Runner size slug (default: ${ARTILLERY_RUNNER_SIZE})
  SSH_USER                   SSH user for droplets (default: ${SSH_USER})
EOF
}

log() {
  printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"
}

fail() {
  log "ERROR: $*"
  exit 1
}

require_env() {
  local name="$1"
  [[ -n "${!name:-}" ]] || fail "Missing required environment variable: ${name}"
}

require_command() {
  local name="$1"
  command -v "$name" >/dev/null 2>&1 || fail "Missing required command: ${name}"
}

detect_operator_cidr() {
  local ip=""

  if ip="$(curl -fsS https://api.ipify.org 2>/dev/null)"; then
    printf '%s/32\n' "$ip"
    return 0
  fi

  if ip="$(curl -fsS https://ifconfig.me 2>/dev/null)"; then
    printf '%s/32\n' "$ip"
    return 0
  fi

  return 1
}

ssh_remote() {
  local host="$1"
  shift
  ssh "${SSH_OPTS[@]}" "${SSH_USER}@${host}" "$@"
}

scp_to() {
  local source="$1"
  local host="$2"
  local destination="$3"
  scp "${SSH_OPTS[@]}" "$source" "${SSH_USER}@${host}:${destination}"
}

scp_from() {
  local host="$1"
  local source="$2"
  local destination="$3"
  scp "${SSH_OPTS[@]}" "${SSH_USER}@${host}:${source}" "$destination"
}

wait_for_ssh() {
  local host="$1"
  local attempts=30
  local sleep_seconds=10
  local i=0

  until ssh_remote "$host" "true" >/dev/null 2>&1; do
    i=$((i + 1))
    if (( i >= attempts )); then
      fail "Timed out waiting for SSH on ${host}"
    fi
    sleep "${sleep_seconds}"
  done
}

wait_for_http_ping() {
  local host="$1"
  local attempts=30
  local sleep_seconds=5
  local i=0
  local url="http://${host}:${RAFT_PORT}/ping"

  until curl -fsS "$url" >/dev/null 2>&1; do
    i=$((i + 1))
    if (( i >= attempts )); then
      fail "Timed out waiting for ${url}"
    fi
    sleep "${sleep_seconds}"
  done
}

wait_for_cluster_write() {
  local attempts=40
  local sleep_seconds=3
  local i=0
  local host=""
  local url=""

  until false; do
    for host in "${RAFT_PUBLIC_IPS[@]}"; do
      url="http://${host}:${RAFT_PORT}/propose"
      if curl -fsS -X POST "$url" >/dev/null 2>&1; then
        log "Cluster accepted writes via ${url}"
        return 0
      fi
    done

    i=$((i + 1))
    if (( i >= attempts )); then
      fail "Timed out waiting for the cluster to accept proposals"
    fi
    sleep "${sleep_seconds}"
  done
}

render_artillery_config() {
  local target_url="$1"
  local destination="$2"

  grep -Eq '^[[:space:]]*target:' "$ARTILLERY_CONFIG" || fail "Artillery config has no target entry: ${ARTILLERY_CONFIG}"

  awk -v target="${target_url}" '
    /^[[:space:]]*target:/ && !done {
      print "  target: \"" target "\""
      done = 1
      next
    }
    { print }
  ' "$ARTILLERY_CONFIG" > "$destination"
}

collect_remote_logs() {
  local index=0
  local public_ip=""

  if [[ -n "${RUNNER_PUBLIC_IP}" ]] && (( ARTIFACTS_FETCHED == 0 )); then
    scp_from "${RUNNER_PUBLIC_IP}" "/opt/go-raft-benchmark/artillery-report.json" "${RUN_DIR}/artillery-report.json" >/dev/null 2>&1 || true
    scp_from "${RUNNER_PUBLIC_IP}" "/opt/go-raft-benchmark/artillery.log" "${RUN_DIR}/artillery.log" >/dev/null 2>&1 || true
  fi

  for public_ip in "${RAFT_PUBLIC_IPS[@]}"; do
    index=$((index + 1))
    ssh_remote "${public_ip}" "journalctl -u go-raft --no-pager -n 4000" > "${RUN_DIR}/raft-node-${index}.journal.log" 2>/dev/null || true
  done
}

destroy_infra() {
  log "Destroying DigitalOcean resources"
  terraform -chdir="${TF_DIR}" destroy -auto-approve -input=false >/dev/null
}

cleanup() {
  local status=$?
  trap - EXIT
  set +e

  if [[ -n "${RUN_DIR}" ]]; then
    mkdir -p "${RUN_DIR}"
  fi

  if (( TERRAFORM_READY == 1 )); then
    collect_remote_logs
    if ! destroy_infra; then
      log "ERROR: terraform destroy failed"
      if (( status == 0 )); then
        status=1
      fi
    fi
  fi

  exit "${status}"
}

while getopts ":c:r:h" opt; do
  case "$opt" in
    c) ARTILLERY_CONFIG="${OPTARG}" ;;
    r) RESULTS_ROOT="${OPTARG}" ;;
    h) usage; exit 0 ;;
    \?) usage; fail "Invalid option: -${OPTARG}" ;;
  esac
done

trap cleanup EXIT

require_command awk
require_command curl
require_command go
require_command scp
require_command ssh
require_command terraform

require_env DO_TOKEN
require_env DO_SSH_KEY_FINGERPRINT

[[ -f "${ARTILLERY_CONFIG}" ]] || fail "Artillery config not found: ${ARTILLERY_CONFIG}"
[[ "${RAFT_NODE_COUNT}" == "3" ]] || fail "This script currently supports exactly 3 raft nodes"

if [[ -z "${OPERATOR_CIDR}" ]]; then
  OPERATOR_CIDR="$(detect_operator_cidr)" || fail "Unable to detect OPERATOR_CIDR automatically; set it explicitly"
fi

RUN_DIR="${RESULTS_ROOT}/${RUN_ID}"
mkdir -p "${RUN_DIR}"

export TF_VAR_do_token="${DO_TOKEN}"
export TF_VAR_do_region="${DO_REGION}"
export TF_VAR_do_image="${DO_IMAGE}"
export TF_VAR_do_ssh_key_fingerprint="${DO_SSH_KEY_FINGERPRINT}"
export TF_VAR_operator_cidr="${OPERATOR_CIDR}"
export TF_VAR_name_prefix="${NAME_PREFIX}-${RUN_ID}"
export TF_VAR_raft_node_count="${RAFT_NODE_COUNT}"
export TF_VAR_raft_port="${RAFT_PORT}"
export TF_VAR_raft_droplet_size="${RAFT_DROPLET_SIZE}"
export TF_VAR_artillery_runner_size="${ARTILLERY_RUNNER_SIZE}"

log "Building Linux raft binary"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "${RUN_DIR}/go-raft" "${REPO_ROOT}"

log "Initializing Terraform"
terraform -chdir="${TF_DIR}" init -input=false >/dev/null
TERRAFORM_READY=1

log "Applying Terraform"
terraform -chdir="${TF_DIR}" apply -auto-approve -input=false >/dev/null

RUNNER_PUBLIC_IP="$(terraform -chdir="${TF_DIR}" output -raw runner_public_ipv4)"
RUNNER_PRIVATE_IP="$(terraform -chdir="${TF_DIR}" output -raw runner_private_ipv4)"
IFS=',' read -r -a RAFT_PUBLIC_IPS <<< "$(terraform -chdir="${TF_DIR}" output -raw raft_public_ipv4_csv)"
IFS=',' read -r -a RAFT_PRIVATE_IPS <<< "$(terraform -chdir="${TF_DIR}" output -raw raft_private_ipv4_csv)"

(( ${#RAFT_PUBLIC_IPS[@]} == 3 )) || fail "Expected 3 raft public IPs, got ${#RAFT_PUBLIC_IPS[@]}"
(( ${#RAFT_PRIVATE_IPS[@]} == 3 )) || fail "Expected 3 raft private IPs, got ${#RAFT_PRIVATE_IPS[@]}"

{
  printf 'run_id=%s\n' "${RUN_ID}"
  printf 'operator_cidr=%s\n' "${OPERATOR_CIDR}"
  printf 'runner_public_ip=%s\n' "${RUNNER_PUBLIC_IP}"
  printf 'runner_private_ip=%s\n' "${RUNNER_PRIVATE_IP}"
  printf 'raft_public_ips=%s\n' "$(IFS=,; echo "${RAFT_PUBLIC_IPS[*]}")"
  printf 'raft_private_ips=%s\n' "$(IFS=,; echo "${RAFT_PRIVATE_IPS[*]}")"
} > "${RUN_DIR}/run.env"

log "Waiting for SSH on all droplets"
for host in "${RAFT_PUBLIC_IPS[@]}" "${RUNNER_PUBLIC_IP}"; do
  wait_for_ssh "${host}"
done

NODES_CSV="$(printf '%s:%s,%s:%s,%s:%s' \
  "${RAFT_PRIVATE_IPS[0]}" "${RAFT_PORT}" \
  "${RAFT_PRIVATE_IPS[1]}" "${RAFT_PORT}" \
  "${RAFT_PRIVATE_IPS[2]}" "${RAFT_PORT}")"
ARTILLERY_TARGET_URL="http://${RAFT_PRIVATE_IPS[0]}:${RAFT_PORT}"
GENERATED_ARTILLERY_CONFIG="${RUN_DIR}/artillery.generated.yaml"

render_artillery_config "${ARTILLERY_TARGET_URL}" "${GENERATED_ARTILLERY_CONFIG}"

log "Bootstrapping raft nodes"
for i in "${!RAFT_PUBLIC_IPS[@]}"; do
  public_ip="${RAFT_PUBLIC_IPS[$i]}"
  private_ip="${RAFT_PRIVATE_IPS[$i]}"

  ssh_remote "${public_ip}" "mkdir -p /usr/local/bin /var/lib/go-raft"
  scp_to "${RUN_DIR}/go-raft" "${public_ip}" "/usr/local/bin/go-raft"

  ssh_remote "${public_ip}" "chmod 0755 /usr/local/bin/go-raft"
  ssh_remote "${public_ip}" "cat > /usr/local/bin/start-go-raft.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exec /usr/local/bin/go-raft \
  -data /var/lib/go-raft \
  -bind-host 0.0.0.0 \
  -advertise-host '${private_ip}' \
  -port '${RAFT_PORT}' \
  -nodes '${NODES_CSV}'
EOF
chmod 0755 /usr/local/bin/start-go-raft.sh"
  ssh_remote "${public_ip}" "cat > /etc/systemd/system/go-raft.service <<'EOF'
[Unit]
Description=go-raft benchmark node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=ENV=PRODUCTION
ExecStart=/usr/local/bin/start-go-raft.sh
Restart=always
RestartSec=2
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now go-raft.service"
done

log "Bootstrapping Artillery runner"
ssh_remote "${RUNNER_PUBLIC_IP}" "apt-get update >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates nodejs npm >/dev/null && npm install -g artillery >/dev/null && mkdir -p /opt/go-raft-benchmark"
scp_to "${GENERATED_ARTILLERY_CONFIG}" "${RUNNER_PUBLIC_IP}" "/opt/go-raft-benchmark/artillery.yaml"

log "Waiting for raft HTTP endpoints"
for host in "${RAFT_PUBLIC_IPS[@]}"; do
  wait_for_http_ping "${host}"
done

wait_for_cluster_write

log "Running Artillery from ${RUNNER_PUBLIC_IP} against ${ARTILLERY_TARGET_URL}"
ssh_remote "${RUNNER_PUBLIC_IP}" "bash -lc 'set -euo pipefail; cd /opt/go-raft-benchmark; artillery run artillery.yaml --output artillery-report.json 2>&1 | tee artillery.log'" | tee "${RUN_DIR}/artillery.stdout.log"

log "Fetching benchmark artifacts"
scp_from "${RUNNER_PUBLIC_IP}" "/opt/go-raft-benchmark/artillery-report.json" "${RUN_DIR}/artillery-report.json"
scp_from "${RUNNER_PUBLIC_IP}" "/opt/go-raft-benchmark/artillery.log" "${RUN_DIR}/artillery.log"
ARTIFACTS_FETCHED=1

log "Benchmark complete; artifacts saved to ${RUN_DIR}"
