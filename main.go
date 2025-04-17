package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
)

// tmp: in-memory cache
var embeddingCache sync.Map

// some globals for the embedding URL + host
var embeddingServerURL string
var embeddingModelHost string

type server struct{}

type healthServer struct{}

func (h *healthServer) Check(ctx context.Context, in *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	log.Printf("[HealthCheck] Received health check request: %+v", in)
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}

func (h *healthServer) Watch(in *healthPb.HealthCheckRequest, srv healthPb.Health_WatchServer) error {
	log.Printf("[HealthWatch] Received watch request: %+v", in)
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

func (s *server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	log.Println("[Process] Starting processing loop")
	for {
		req, err := srv.Recv()
		if err == io.EOF {
			log.Println("[Process] Received EOF, terminating processing loop")
			return nil
		}
		if err != nil {
			log.Printf("[Process] Error receiving request: %v", err)
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}
		log.Printf("[Process] Received request: %+v", req)

		var resp *extProcPb.ProcessingResponse

		switch r := req.Request.(type) {

		// pass through request headers unaltered
		case *extProcPb.ProcessingRequest_RequestHeaders:
			log.Println("[Process] Processing RequestHeaders")
			resp = &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestHeaders{
					RequestHeaders: &extProcPb.HeadersResponse{},
				},
			}
			log.Println("[Process] RequestHeaders processed, passing through unchanged")

		// process the request body
		case *extProcPb.ProcessingRequest_RequestBody:
			log.Println("[Process] Processing RequestBody for embedding lookup")
			rb := r.RequestBody
			if !rb.EndOfStream {
				log.Println("[Process] RequestBody not complete, continuing to buffer")
				resp = &extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_RequestBody{
						RequestBody: &extProcPb.BodyResponse{},
					},
				}
				// send the incomplete body response, continue with the next msg
				if err := srv.Send(resp); err != nil {
					log.Printf("[Process] Error sending response: %v", err)
				}
				continue
			}

			var reqPayload map[string]interface{}
			if err := json.Unmarshal(rb.Body, &reqPayload); err != nil {
				log.Printf("[Process] Failed to unmarshal request JSON: %v", err)
				// pass through unchanged if the JSON cannot be decoded
				resp = &extProcPb.ProcessingResponse{
					Response: &extProcPb.ProcessingResponse_RequestBody{
						RequestBody: &extProcPb.BodyResponse{},
					},
				}
				break
			}

			// if a "prompt" field exists, perform an embedding lookup
			if promptRaw, ok := reqPayload["prompt"]; ok {
				prompt, ok := promptRaw.(string)
				if !ok {
					log.Println("[Process] 'prompt' field is not a string, skipping embedding lookup")
				} else {
					// cache hit check
					if _, found := embeddingCache.Load(prompt); found {
						log.Println("[Process] Cache hit for prompt embedding")
					} else {
						log.Println("[Process] Cache miss, calling embedding model server")
						if embeddingServerURL == "" {
							log.Println("[Process] EMBEDDING_MODEL_SERVER env var not set; skipping embedding lookup")
						} else {
							// call embedding
							embedReqPayload := map[string]interface{}{
								"instances": []string{prompt},
							}
							bodyData, err := json.Marshal(embedReqPayload)
							if err != nil {
								log.Printf("[Process] Error marshaling embedding request payload: %v", err)
							} else {
								log.Printf("[Process] Calling embedding model server at URL: %s", embeddingServerURL)
								log.Printf("[Process] Embedding request payload: %s", string(bodyData))
								client := &http.Client{Timeout: 10 * time.Second}
								httpReq, err := http.NewRequest("POST", embeddingServerURL, bytes.NewReader(bodyData))
								if err != nil {
									log.Printf("[Process] Error creating HTTP request: %v", err)
								} else {
									httpReq.Header.Set("Content-Type", "application/json")
									if embeddingModelHost != "" {
										httpReq.Host = embeddingModelHost
										log.Printf("[Process] Setting Host header to: %s", embeddingModelHost)
									}
									for key, values := range httpReq.Header {
										log.Printf("[Process] Embedding Request Header: %s: %v", key, values)
									}
									httpResp, err := client.Do(httpReq)
									if err != nil {
										log.Printf("[Process] Error calling embedding model server: %v", err)
									} else {
										log.Printf("[Process] Embedding server response status: %s", httpResp.Status)
										for key, values := range httpResp.Header {
											log.Printf("[Process] Embedding Response Header: %s: %v", key, values)
										}
										respBytes, err := io.ReadAll(httpResp.Body)
										if err != nil {
											log.Printf("[Process] Error reading embedding response: %v", err)
										}
										httpResp.Body.Close()
										log.Printf("[Process] Embedding server returned body: %s", string(respBytes))
										var embedResponse struct {
											Predictions [][]float64 `json:"predictions"`
										}
										if err := json.Unmarshal(respBytes, &embedResponse); err != nil {
											log.Printf("[Process] Error parsing embedding response: %v", err)
										} else if len(embedResponse.Predictions) == 0 {
											log.Println("[Process] Received empty predictions from embedding model")
										} else {
											embedding := embedResponse.Predictions[0]
											log.Printf("[Process] Received embedding: %+v", embedding)
											embeddingCache.Store(prompt, embedding)
										}
									}
								}
							}
						}
					}
				}
			} else {
				log.Println("[Process] No 'prompt' field found in request; skipping embedding lookup")
			}

			// always pass through the original request body unaltered
			resp = &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_RequestBody{
					RequestBody: &extProcPb.BodyResponse{},
				},
			}
			log.Println("[Process] RequestBody processed; original payload passed through unchanged")

		case *extProcPb.ProcessingRequest_ResponseHeaders:
			log.Println("[Process] Processing ResponseHeaders, passing through unchanged")
			resp = &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseHeaders{
					ResponseHeaders: &extProcPb.HeadersResponse{},
				},
			}
			log.Println("[Process] ResponseHeaders processed, passing through unchanged")

		case *extProcPb.ProcessingRequest_ResponseBody:
			log.Println("[Process] Processing ResponseBody, passing through unchanged")
			resp = &extProcPb.ProcessingResponse{
				Response: &extProcPb.ProcessingResponse_ResponseBody{
					ResponseBody: &extProcPb.BodyResponse{},
				},
			}
			log.Println("[Process] ResponseBody processed, passing through unchanged")

		default:
			log.Printf("[Process] Received unrecognized request type: %+v", r)
			resp = &extProcPb.ProcessingResponse{}
		}

		if err := srv.Send(resp); err != nil {
			log.Printf("[Process] Error sending response: %v", err)
		} else {
			log.Printf("[Process] Sent response: %+v", resp)
		}
	}
}

func main() {
	embeddingServerURL = os.Getenv("EMBEDDING_MODEL_SERVER")
	if embeddingServerURL == "" {
		log.Println("[Main] WARNING: EMBEDDING_MODEL_SERVER env var not set; external embedding lookup will be skipped")
	} else {
		log.Printf("[Main] Using embedding model server at: %s", embeddingServerURL)
	}
	embeddingModelHost = os.Getenv("EMBEDDING_MODEL_HOST")
	if embeddingModelHost != "" {
		log.Printf("[Main] Using embedding model host: %s", embeddingModelHost)
	}

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("[Main] Failed to listen: %v", err)
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, &server{})
	healthPb.RegisterHealthServer(s, &healthServer{})
	log.Println("[Main] Starting gRPC server on port :50051")

	// graceful shutdown
	gracefulStop := make(chan os.Signal, 1)
	signal.Notify(gracefulStop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-gracefulStop
		log.Println("[Main] Received shutdown signal, exiting after 1 second")
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()

	if err := s.Serve(lis); err != nil {
		log.Fatalf("[Main] Failed to serve: %v", err)
	}
}
