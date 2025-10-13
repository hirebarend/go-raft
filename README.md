You're an expert copy writer for technical personal blog targeted at a software engineering audience. Use the draft below as the outline of a blog which can I can post on my personal website called barenderasmus.com. I like the style in which Tigerbeetle and DuckDB and Cloudflare writes their blogs. Use it as a guide while keeping it authentic and personal. The blog needs have a highly likelyhood of being interesting to the target audience, SEO friendly and centered around a core theme. 

Back in 2016, I was working on a NCache to Redis migration. The catch, it was an application that directly depended on NCache functionality and everything was running on dedicated servers. Some of them literal boxes you can walk over to and kick.

Redis was fairly new around that time and very much a host your own type of solution unless you had excess money. I was tasked with learning all about Redis with the goal of setting up an production cluster. This was during a time where Kubernetes was just released, cloud providers didnt support it and the best that could providers would offer was a few virtual machines with Docker Swarm.

Redis just deprecate their sentinal approach to clustering and moved to a gossip protocol. There I was, almost somewhat of a teenager, learning Ruby scripts and writing Shell scripts to automatically install Redis and setup the Redis cluster.

While this was happening, Raft, the consensus algorithm developed by Diego Ongaro was starting to gain traction and this is what caught my attention and what stuck with me for the next few years.

Since then I've kept trying to implement the Raft consensus algorithm by following the white paper. I've written it in C#, TypeScript and now in Go. 

This time around, my goal was the implement it in an easy to understand and follow manner. The white paper explains it really nicely and one can grasp it fairly easilily but once you start implementing, it can become complex, which in my views goes against Diego's key princiliples of why the algortihm was developed in the first place. It was primaryily to be easy to understand compared to Paxos which even Google agreed is far too complex to implement correctly.

At it's core, Raft consists of two RPC calls, one used for voting and the other for replcation. There are three state, candidate, follower and leader. I've approach the implementation in the following way to make it easy to understand. Each state's implementation is contained and the handling of their RPC calls. This makes it clear on how each action for a given state is treated.

The three states are contained within a wrapper which contains the properties such as the log for the raft implemenation. This wrapper depends on a single state which the node can be in such as follower. Additionally, to make this even more simpler and clear, every action in the implementtion has a meaningful and descriptive method name, variable name, etc. which might go against the coding standards or common practices on the Go langauges but it helps with readiblity.

Here is the repository of my implemenation in Go, https://....


```bash
go build . 

chmod +x ./scripts/start-cluster.sh

./scripts/start-cluster.sh
```

```bash
go test ./...

go test ./counter -bench=. -benchmem -benchtime=20s
```