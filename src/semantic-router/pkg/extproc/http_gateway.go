package extproc

import (
    "bytes"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/vllm-project/semantic-router/src/semantic-router/pkg/config"
    "github.com/vllm-project/semantic-router/src/semantic-router/pkg/observability"
)

// StartHTTPGateway starts a simple HTTP gateway that accepts OpenAI-compatible
// /v1/chat/completions requests and forwards them to configured vLLM endpoints.
// This gateway implements a subset of router features (non-streaming only).
// It is intended for deployments that use an ingress controller (NGINX, ALB, ...)
func StartHTTPGateway(configPath string, port int) error {
    // Build router instance (loads config and classifiers)
    router, err := NewOpenAIRouter(configPath)
    if err != nil {
        return fmt.Errorf("failed to create router for http gateway: %w", err)
    }

    // Also load parsed config for endpoint selection convenience
    cfg, err := config.LoadConfig(configPath)
    if err != nil {
        return fmt.Errorf("failed to load config for http gateway: %w", err)
    }

    mux := http.NewServeMux()

    // Simple models endpoint (GET /v1/models)
    mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
        // Reuse existing classification API behavior for model listing
        if cfg.IncludeConfigModelsInList {
            // Delegate to existing API model handler if desired in future; for now return 204
            w.WriteHeader(http.StatusNoContent)
            return
        }
        // Default: show auto model alias
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{"object":"list","data":[{"id":"MoM","object":"model","created":0,"owned_by":"semantic-router"}]}`))
    })

    // Chat completions handler (non-streaming)
    mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }

        // Read full body
        body, err := io.ReadAll(r.Body)
        if err != nil {
            http.Error(w, "failed to read request body", http.StatusBadRequest)
            return
        }

        // If streaming requested we don't support it in this simple gateway
        if extractStreamParam(body) {
            http.Error(w, "streaming responses are not supported by the nginx-only HTTP gateway; use Envoy ExtProc for full streaming support", http.StatusBadRequest)
            return
        }

        // Parse request to extract user content and model
        openAIReq, err := parseOpenAIRequest(body)
        if err != nil {
            http.Error(w, "invalid OpenAI request payload", http.StatusBadRequest)
            return
        }

        // Determine model to use
        requestedModel := openAIReq.Model
        var selectedModel string

        if requestedModel == "auto" || requestedModel == "MoM" || requestedModel == "" {
            // Classify and determine model using router
            // Use user content extracted by helper
            userContent, _ := extractUserAndNonUserContent(openAIReq)
            bestModel, _ := router.ClassifyAndDetermineReasoningMode(userContent)
            selectedModel = bestModel
        } else {
            selectedModel = requestedModel
        }

        // Resolve endpoint address for selected model
        endpointAddr, ok := cfg.SelectBestEndpointAddressForModel(selectedModel)
        if !ok || endpointAddr == "" {
            http.Error(w, "no backend endpoint available for selected model", http.StatusServiceUnavailable)
            return
        }

        // Forward the request to the selected vLLM endpoint
        forwardURL := fmt.Sprintf("http://%s/v1/chat/completions", endpointAddr)
        observability.Infof("Forwarding request for model=%s to %s", selectedModel, forwardURL)

        client := &http.Client{Timeout: 120 * time.Second}
        req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, forwardURL, bytes.NewReader(body))
        if err != nil {
            http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
            return
        }
        // Copy content-type
        req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

        resp, err := client.Do(req)
        if err != nil {
            observability.Warnf("Upstream request error: %v", err)
            http.Error(w, "upstream request failed", http.StatusBadGateway)
            return
        }
        defer resp.Body.Close()

        // Copy status and headers
        for k, vv := range resp.Header {
            for _, v := range vv {
                w.Header().Add(k, v)
            }
        }
        w.WriteHeader(resp.StatusCode)

        // Stream body back to client (non-streaming upstream will work fine)
        if _, err := io.Copy(w, resp.Body); err != nil {
            observability.Warnf("Failed to copy upstream response: %v", err)
        }
    })

    addr := fmt.Sprintf(":%d", port)
    observability.Infof("Starting HTTP gateway on %s (nginx-only mode, non-streaming)", addr)
    return http.ListenAndServe(addr, mux)
}
