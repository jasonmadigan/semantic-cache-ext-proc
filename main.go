package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typeV3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
)

// CacheEntry holds prompt, its embedding, and the cached response
// semanticCache builds over time; embeddingCache is legacy exact-match

type CacheEntry struct {
	Prompt     string
	Embedding  []float64
	Response   []byte
	CreateTime time.Time
}

var (
	semanticCache       []*CacheEntry
	embeddingCache      sync.Map
	embeddingServerURL  string
	embeddingModelHost  string
	similarityThreshold = 0.75
	cacheMutex          sync.Mutex
)

func cosineSimilarity(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func findMostSimilarPrompt(vec []float64) (*CacheEntry, float64) {
	cacheMutex.Lock()
	defer cacheMutex.Unlock()
	var best *CacheEntry
	var bestSim float64
	for _, e := range semanticCache {
		if s := cosineSimilarity(vec, e.Embedding); s > bestSim {
			bestSim, best = s, e
		}
	}
	return best, bestSim
}

type server struct{}

type healthServer struct{}

func (h *healthServer) Check(ctx context.Context, in *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}
func (h *healthServer) Watch(in *healthPb.HealthCheckRequest, srv healthPb.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

func (s *server) Process(srv extProcPb.ExternalProcessor_ProcessServer) error {
	log.Println("[Process] Starting processing loop")
	var lastPrompt string

	for {
		req, err := srv.Recv()
		if err == io.EOF {
			log.Println("[Process] EOF, exiting")
			return nil
		} else if err != nil {
			log.Printf("[Process] Recv error: %v", err)
			return status.Errorf(codes.Unknown, "recv error: %v", err)
		}

		var resp *extProcPb.ProcessingResponse
		log.Printf("[Process] Handling %T", req.Request)

		switch r := req.Request.(type) {

		case *extProcPb.ProcessingRequest_RequestHeaders:
			resp = &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_RequestHeaders{RequestHeaders: &extProcPb.HeadersResponse{}}}

		case *extProcPb.ProcessingRequest_RequestBody:
			rb := r.RequestBody
			log.Printf("[Process] RequestBody, end_of_stream=%v", rb.EndOfStream)
			if !rb.EndOfStream {
				srv.Send(&extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_RequestBody{RequestBody: &extProcPb.BodyResponse{}}})
				continue
			}

			// parse JSON once complete
			var pl map[string]interface{}
			if err := json.Unmarshal(rb.Body, &pl); err != nil {
				log.Printf("[Process] JSON parse failed: %v", err)
				resp = &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_RequestBody{RequestBody: &extProcPb.BodyResponse{}}}
				break
			}

			// extract prompt
			if raw, ok := pl["prompt"]; ok {
				if prompt, ok2 := raw.(string); ok2 {
					log.Printf("[Process] Prompt: %s", prompt)
					lastPrompt = prompt

					// lookup embedding
					var emb []float64
					if v, ok3 := embeddingCache.Load(prompt); ok3 {
						emb = v.([]float64)
						log.Println("[Process] Legacy cache hit for embedding")
					} else if embeddingServerURL != "" {
						log.Println("[Process] Cache miss, fetching embedding from", embeddingServerURL)
						reqMap := map[string]interface{}{"instances": []string{prompt}}
						data, _ := json.Marshal(reqMap)
						client := &http.Client{Timeout: 10 * time.Second}
						httpReq, err := http.NewRequest("POST", embeddingServerURL, bytes.NewReader(data))
						if err != nil {
							log.Printf("[Process] HTTP request err: %v", err)
						} else {
							httpReq.Header.Set("Content-Type", "application/json")
							if embeddingModelHost != "" {
								httpReq.Host = embeddingModelHost
								log.Printf("[Process] Set Host header: %s", embeddingModelHost)
							}
							httpResp, err := client.Do(httpReq)
							if err != nil {
								log.Printf("[Process] Fetch embedding err: %v", err)
							} else {
								log.Printf("[Process] Embedding responded: %s", httpResp.Status)
								b, _ := io.ReadAll(httpResp.Body)
								httpResp.Body.Close()
								var o struct{ Predictions [][]float64 }
								if json.Unmarshal(b, &o) == nil && len(o.Predictions) > 0 {
									emb = o.Predictions[0]
									embeddingCache.Store(prompt, emb)
									log.Printf("[Process] Stored new embedding len=%d", len(emb))
								}
							}
						}
					}

					// similarity logging
					if len(emb) > 0 {
						log.Printf("[Process] Semantic lookup on %d entries", len(semanticCache))
						e, sim := findMostSimilarPrompt(emb)
						if e != nil {
							log.Printf("[Process] Best candidate: %s with similarity=%.3f (threshold=%.3f)", e.Prompt, sim, similarityThreshold)
							if sim >= similarityThreshold && e.Response != nil {
								log.Printf("[Process] similarity %.3f >= threshold %.3f; cache HIT", sim, similarityThreshold)
								srv.Send(&extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_ImmediateResponse{ImmediateResponse: &extProcPb.ImmediateResponse{Status: &typeV3.HttpStatus{Code: 200}, Body: e.Response}}})
								continue
							} else {
								log.Printf("[Process] similarity %.3f < threshold %.3f; no cache hit", sim, similarityThreshold)
							}
						} else {
							log.Println("[Process] semanticCache empty; no candidate to compare")
						}
					}
				}
			}

			// pass through request body
			resp = &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_RequestBody{RequestBody: &extProcPb.BodyResponse{}}}

		case *extProcPb.ProcessingRequest_ResponseHeaders:
			resp = &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_ResponseHeaders{ResponseHeaders: &extProcPb.HeadersResponse{}}}

		case *extProcPb.ProcessingRequest_ResponseBody:
			rb := r.ResponseBody
			log.Printf("[Process] ResponseBody, end_of_stream=%v", rb.EndOfStream)
			if rb.EndOfStream && lastPrompt != "" {
				cacheMutex.Lock()
				if embI, ok := embeddingCache.Load(lastPrompt); ok {
					emb := embI.([]float64)
					semanticCache = append(semanticCache, &CacheEntry{Prompt: lastPrompt, Embedding: emb, Response: rb.Body, CreateTime: time.Now()})
					log.Printf("[Process] Added semanticCache entry for %s", lastPrompt)
				}
				cacheMutex.Unlock()
			}
			resp = &extProcPb.ProcessingResponse{Response: &extProcPb.ProcessingResponse_ResponseBody{ResponseBody: &extProcPb.BodyResponse{}}}

		default:
			resp = &extProcPb.ProcessingResponse{}
		}

		// send response
		srv.Send(resp)
	}
}

func main() {
	embeddingServerURL = os.Getenv("EMBEDDING_MODEL_SERVER")
	embeddingModelHost = os.Getenv("EMBEDDING_MODEL_HOST")
	log.Printf("[Main] EMBEDDING_MODEL_SERVER=%s", embeddingServerURL)
	log.Printf("[Main] EMBEDDING_MODEL_HOST=%s", embeddingModelHost)
	if ts := os.Getenv("SIMILARITY_THRESHOLD"); ts != "" {
		if v, err := strconv.ParseFloat(ts, 64); err == nil {
			similarityThreshold = v
			log.Printf("[Main] similarityThreshold=%.3f", similarityThreshold)
		}
	}

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("[Main] Listen error: %v", err)
	}
	s := grpc.NewServer()
	extProcPb.RegisterExternalProcessorServer(s, &server{})
	healthPb.RegisterHealthServer(s, &healthServer{})
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		<-c
		os.Exit(0)
	}()
	log.Println("[Main] ext_proc listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("[Main] Serve error: %v", err)
	}
}
