# Using Semantic Router with Nginx Ingress (No Envoy)

This directory contains Kubernetes manifests to deploy vLLM Semantic Router without requiring Envoy ExtProc support. This router just exposes a simple lightweight http endpoints to use the semantic router but limited capabilities

## Features and Limitations

Key Features:
- Standard OpenAI API endpoints (/v1/chat/completions)
- Model classification and routing based on user query
- Automatic model selection when using "MoM" or "auto" model
- Choose between multiple vLLM backend endpoints
- CORS support via nginx 

Limitations compared to Envoy deployment:
1. No streaming support (`stream: true` will return an error)
2. Simple request/response proxying 
3. Basic nginx-level retries and timeouts only

## Quick Start

1. Deploy the router with HTTP gateway enabled:

```bash
kubectl apply -f deployment.yaml
```

2. Create nginx ingress (adapt host/paths as needed):

```bash
kubectl apply -f ingress.yaml
```

3. Test the deployment:

```bash
# Get the ingress endpoint
INGRESS_HOST=$(kubectl get ingress -n vllm-semantic-router-system semantic-router -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Test models list endpoint
curl http://$INGRESS_HOST/v1/models

# Test chat completion (non-streaming)
curl -X POST http://$INGRESS_HOST/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "MoM",
    "messages": [{"role": "user", "content": "What is the derivative of x^2?"}]
  }'
```

## Configuration

The HTTP gateway uses the same configuration as the standard router (mounted configmap). Key settings:

```yaml
# Add to config.yaml
vllm_endpoints:
- name: endpoint1
  address: vllm-model1-service
  port: 8000
  weight: 1
- name: endpoint2
  address: vllm-model2-service
  port: 8000
  weight: 1

model_config:
  model1:
    preferred_endpoints: ["endpoint1"]
  model2:
    preferred_endpoints: ["endpoint2"]
```

## Monitoring

The HTTP gateway reports metrics via the existing /metrics endpoint (port 9190). Additional metric labels indicate whether requests were handled by the gateway vs. extproc server.