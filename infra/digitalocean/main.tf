provider "digitalocean" {
  token = var.do_token
}

locals {
  cluster_tag = "${var.name_prefix}-cluster"
  runner_tag  = "${var.name_prefix}-runner"
}

resource "digitalocean_tag" "cluster" {
  name = local.cluster_tag
}

resource "digitalocean_tag" "runner" {
  name = local.runner_tag
}

resource "digitalocean_vpc" "raft" {
  name     = "${var.name_prefix}-vpc"
  region   = var.do_region
  ip_range = var.vpc_ip_range
}

resource "digitalocean_droplet" "raft_nodes" {
  count = var.raft_node_count

  name     = format("%s-node-%02d", var.name_prefix, count.index + 1)
  region   = var.do_region
  size     = var.raft_droplet_size
  image    = var.do_image
  vpc_uuid = digitalocean_vpc.raft.id
  ssh_keys = [var.do_ssh_key_fingerprint]
  tags     = [digitalocean_tag.cluster.name]
}

resource "digitalocean_droplet" "artillery_runner" {
  name = "${var.name_prefix}-runner"

  region   = var.do_region
  size     = var.artillery_runner_size
  image    = var.do_image
  vpc_uuid = digitalocean_vpc.raft.id
  ssh_keys = [var.do_ssh_key_fingerprint]
  tags     = [digitalocean_tag.runner.name]
}

resource "digitalocean_firewall" "raft_nodes" {
  name = "${var.name_prefix}-raft-fw"
  tags = [digitalocean_tag.cluster.name]

  inbound_rule {
    protocol         = "tcp"
    port_range       = "22"
    source_addresses = [var.operator_cidr]
  }

  inbound_rule {
    protocol    = "tcp"
    port_range  = tostring(var.raft_port)
    source_tags = [digitalocean_tag.cluster.name, digitalocean_tag.runner.name]
  }

  inbound_rule {
    protocol         = "tcp"
    port_range       = tostring(var.raft_port)
    source_addresses = [var.operator_cidr]
  }

  outbound_rule {
    protocol              = "tcp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "udp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "icmp"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
}

resource "digitalocean_firewall" "artillery_runner" {
  name = "${var.name_prefix}-runner-fw"
  tags = [digitalocean_tag.runner.name]

  inbound_rule {
    protocol         = "tcp"
    port_range       = "22"
    source_addresses = [var.operator_cidr]
  }

  outbound_rule {
    protocol              = "tcp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "udp"
    port_range            = "1-65535"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }

  outbound_rule {
    protocol              = "icmp"
    destination_addresses = ["0.0.0.0/0", "::/0"]
  }
}
