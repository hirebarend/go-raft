```bash
go build . 

./scripts/example.sh
```

```bash
go test ./...

go test ./counter -bench=. -benchmem -benchtime=20s
```