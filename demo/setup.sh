#!/usr/bin/env bash
# Some log tailing for a demo
#
# Setup per README, then run

set -euo pipefail

NS=${NS:-default}

SERVICES=(
  "llm-inference:serving.kserve.io/inferenceservice=huggingface-llm"
  "llm-embedding:serving.kserve.io/inferenceservice=embedding-model"
)

LOCAL_PROC_NICK="semantic-cache-ext-proc"
LOCAL_PROC_CMD="../semantic-cache-ext-proc"

COLORS=(31 32 33 34 35 36)

cleanup() { pkill -P $$ || true; }
trap cleanup INT TERM EXIT

# stream some pod logs
i=0
for ENTRY in "${SERVICES[@]}"; do
  NAME=${ENTRY%%:*}
  SEL=${ENTRY#*:}
  COLOR=${COLORS[$((i % ${#COLORS[@]}))]}
  ((i++))

  kubectl logs -n "$NS" -f \
          -l "$SEL" --max-log-requests=1 --prefix 2>&1 \
  | sed -u "s/^/$(printf '\033[%sm[%s]\033[0m ' "$COLOR" "$NAME")/" &
done

# run semantic-cache-ext-proc w/ decoration
PROC_COLOR=${COLORS[$((i % ${#COLORS[@]}))]}

(
  GATEWAY_HOST=$(kubectl get gateway -n kserve kserve-ingress-gateway \
                 -o jsonpath='{.status.addresses[0].value}')
  SERVICE_HOSTNAME=$(kubectl get inferenceservice embedding-model \
                     -o jsonpath='{.status.url}' | cut -d/ -f3)

  export EMBEDDING_MODEL_HOST="$SERVICE_HOSTNAME"
  export EMBEDDING_MODEL_SERVER="http://$GATEWAY_HOST/v1/models/embedding-model:predict"

  exec $LOCAL_PROC_CMD
) 2>&1 \
  | sed -u "s/^/$(printf '\033[%sm[%s]\033[0m ' "$PROC_COLOR" "$LOCAL_PROC_NICK")/" &

wait
