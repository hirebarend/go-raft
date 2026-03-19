output "raft_public_ipv4s" {
  description = "Public IPv4 addresses of the raft nodes."
  value       = digitalocean_droplet.raft_nodes[*].ipv4_address
}

output "raft_public_ipv4_csv" {
  description = "Comma-separated public IPv4 addresses of the raft nodes."
  value       = join(",", digitalocean_droplet.raft_nodes[*].ipv4_address)
}

output "raft_private_ipv4s" {
  description = "Private IPv4 addresses of the raft nodes."
  value       = digitalocean_droplet.raft_nodes[*].ipv4_address_private
}

output "raft_private_ipv4_csv" {
  description = "Comma-separated private IPv4 addresses of the raft nodes."
  value       = join(",", digitalocean_droplet.raft_nodes[*].ipv4_address_private)
}

output "raft_nodes" {
  description = "Combined metadata for raft nodes."
  value = [
    for droplet in digitalocean_droplet.raft_nodes : {
      name       = droplet.name
      public_ip  = droplet.ipv4_address
      private_ip = droplet.ipv4_address_private
    }
  ]
}

output "runner_public_ipv4" {
  description = "Public IPv4 address of the Artillery runner."
  value       = digitalocean_droplet.artillery_runner.ipv4_address
}

output "runner_private_ipv4" {
  description = "Private IPv4 address of the Artillery runner."
  value       = digitalocean_droplet.artillery_runner.ipv4_address_private
}
