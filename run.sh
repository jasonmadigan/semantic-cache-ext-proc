#!/bin/bash

GATEWAY_HOST=$(kubectl get gateway -n kserve kserve-ingress-gateway -o jsonpath='{.status.addresses[0].value}')
SERVICE_HOSTNAME=$(kubectl get inferenceservice embedding-model -o jsonpath='{.status.url}' | cut -d "/" -f 3)

export EMBEDDING_MODEL_HOST="$SERVICE_HOSTNAME"
export EMBEDDING_MODEL_SERVER="http://$GATEWAY_HOST/v1/models/embedding-model:predict"
./semantic-cache-ext-proc


GATEWAY_HOST=$(kubectl get gateway -n kserve kserve-ingress-gateway -o jsonpath='{.status.addresses[0].value}')
SERVICE_HOSTNAME=$(kubectl get inferenceservice huggingface-llm -o jsonpath='{.status.url}' | cut -d "/" -f 3)

echo "Calling SmolLM LLM ..."
curl -v http://$GATEWAY_HOST/openai/v1/completions \
  -H "content-type: application/json" \
  -H "Host: $SERVICE_HOSTNAME" \
  -d '{"model": "llm", "prompt": "What is Kubernetes", "stream": false, "max_tokens": 10}'