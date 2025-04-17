# `ext_proc` for LLM Token Usage data

An Envoy `ext_proc` filter for adding Semantic Caching to LLM inference requests

TODO: flow-chart

TODO: env-vars

TODO: how to run

Sample caching with `GPTCache`:

```bash
docker pull zilliz/gptcache:latest
docker run -p 8000:8000 -it zilliz/gptcache:latest
```


```bash
docker build -t token-ext-proc .
docker run -p 50051:50051 token-ext-proc

kubectl apply -f filter.yaml

GATEWAY_HOST=$(kubectl get gateway -n kserve kserve-ingress-gateway -o jsonpath='{.status.addresses[0].value}')
SERVICE_HOSTNAME=$(kubectl get inferenceservice huggingface-llm -o jsonpath='{.status.url}' | cut -d "/" -f 3)

curl -v http://$GATEWAY_HOST/openai/v1/completions \
  -H "content-type: application/json" \
  -H "Host: $SERVICE_HOSTNAME" \
  -d '{"model": "llm", "prompt": "What is Kubernetes", "stream": false, "max_tokens": 10}'
*   Trying 192.168.97.4:80...
* Connected to 192.168.97.4 (192.168.97.4) port 80
> POST /openai/v1/completions HTTP/1.1
> Host: huggingface-llm-default.example.com
> User-Agent: curl/8.7.1
> Accept: */*
> content-type: application/json
> Content-Length: 83
>
* upload completely sent off: 83 bytes
< HTTP/1.1 200 OK
< date: Fri, 04 Apr 2025 10:39:41 GMT
< server: istio-envoy
< content-length: 387
< content-type: application/json
< x-envoy-upstream-service-time: 13725
< x-kuadrant-openai-prompt-tokens: 5
< x-kuadrant-openai-total-tokens: 15
< x-kuadrant-openai-completion-tokens: 10
<
* Connection #0 to host 192.168.97.4 left intact
{"id":"12972888-f5f5-4fcd-8f19-1ff1327756c8","object":"text_completion","created":1743763196,"model":"llm","choices":[{"index":0,"text":"?\n\nKubernetes is a container orchestr","logprobs":null,"finish_reason":"length","stop_reason":null,"prompt_logprobs":null}],"usage":{"prompt_tokens":5,"total_tokens":15,"completion_tokens":10,"prompt_tokens_details":null},"system_fingerprint":null}
```