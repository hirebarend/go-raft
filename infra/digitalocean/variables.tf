variable "do_token" {
  description = "DigitalOcean API token."
  type        = string
  sensitive   = true
}

variable "do_region" {
  description = "DigitalOcean region for all benchmark resources."
  type        = string
  default     = "nyc3"
}

variable "do_image" {
  description = "Droplet image slug."
  type        = string
  default     = "ubuntu-24-04-x64"
}

variable "do_ssh_key_fingerprint" {
  description = "Existing DigitalOcean SSH key fingerprint or ID."
  type        = string
}

variable "operator_cidr" {
  description = "Operator workstation CIDR allowed to SSH and hit the raft HTTP port."
  type        = string
}

variable "name_prefix" {
  description = "Prefix applied to DigitalOcean resource names."
  type        = string
  default     = "go-raft-bench"
}

variable "raft_node_count" {
  description = "Number of raft nodes to provision."
  type        = number
  default     = 3
}

variable "raft_port" {
  description = "HTTP port exposed by the raft process."
  type        = number
  default     = 8081
}

variable "raft_droplet_size" {
  description = "DigitalOcean size slug for raft nodes."
  type        = string
  default     = "s-1vcpu-1gb"
}

variable "artillery_runner_size" {
  description = "DigitalOcean size slug for the Artillery runner."
  type        = string
  default     = "s-1vcpu-1gb"
}

variable "vpc_ip_range" {
  description = "Private VPC CIDR block used for benchmark droplets."
  type        = string
  default     = "10.42.0.0/24"
}
