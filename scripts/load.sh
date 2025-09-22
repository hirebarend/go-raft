curl -X POST http://localhost:8082/propose \
  -H "Content-Type: application/json" \
  -d '{}'

npx artillery run artillery.yaml