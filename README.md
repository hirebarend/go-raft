```bash
go build . 

chmod +x ./scripts/start-cluster.sh

./scripts/start-cluster.sh
```

```bash
go test ./...

go test ./counter -bench=. -benchmem -benchtime=20s
```