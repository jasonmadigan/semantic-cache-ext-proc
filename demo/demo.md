# Script

```bash
./setup.sh
```


```bash
GATEWAY_HOST=$(kubectl get gateway -n kserve kserve-ingress-gateway -o jsonpath='{.status.addresses[0].value}')
SERVICE_HOSTNAME=$(kubectl get inferenceservice huggingface-llm -o jsonpath='{.status.url}' | cut -d "/" -f 3)
clear
```

```bash
# 1. Cache miss
curl -v http://$GATEWAY_HOST/openai/v1/completions \
  -H "content-type: application/json" \
  -H "Host: $SERVICE_HOSTNAME" \
  -d '{"model": "llm", "prompt": "What is Kubernetes", "stream": false, "max_tokens": 10}'
```


```bash
# 2. Cos-sim ~0.75
curl -v http://$GATEWAY_HOST/openai/v1/completions \
  -H "content-type: application/json" \
  -H "Host: $SERVICE_HOSTNAME" \
  -d '{"model": "llm", "prompt": "Kubernetes what is it anyway", "stream": false, "max_tokens": 10}'
```


```bash
# 3. Same prompt, quick response
curl -v http://$GATEWAY_HOST/openai/v1/completions \
  -H "content-type: application/json" \
  -H "Host: $SERVICE_HOSTNAME" \
  -d '{"model": "llm", "prompt": "Kubernetes what is it anyway", "stream": false, "max_tokens": 10}'
```

