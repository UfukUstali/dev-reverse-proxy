package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type Client struct {
	ID            string `json:"id"`
	Port          int    `json:"port"`
	Subdomain     string
	LastHeartbeat time.Time
}

type TraefikConfig struct {
	HTTP struct {
		Routers  map[string]Router  `yaml:"routers"`
		Services map[string]Service `yaml:"services"`
	} `yaml:"http"`
}

type Router struct {
	EntryPoints []string `yaml:"entryPoints"`
	Rule        string   `yaml:"rule"`
	Service     string   `yaml:"service"`
}

type Service struct {
	LoadBalancer LoadBalancer `yaml:"loadBalancer"`
}

type LoadBalancer struct {
	Servers []Server `yaml:"servers"`
}

type Server struct {
	URL string `yaml:"url"`
}

type ServerManager struct {
	clients          map[string]*Client
	mu               sync.RWMutex
	configDir        string
	heartbeatTimeout time.Duration
}

type RegisterRequest struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

type RegisterResponse struct {
	Status  string `json:"status"`
	URL     string `json:"url"`
	Message string `json:"message,omitempty"`
}

func NewServerManager(configDir string, heartbeatTimeout time.Duration) *ServerManager {
	return &ServerManager{
		clients:          make(map[string]*Client),
		configDir:        configDir,
		heartbeatTimeout: heartbeatTimeout,
	}
}

func (sm *ServerManager) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(RegisterResponse{
			Status:  "error",
			Message: "invalid json",
		})
		return
	}

	if !validateSubdomain(req.ID) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(RegisterResponse{
			Status:  "error",
			Message: "invalid subdomain format",
		})
		return
	}

	if req.Port < 1 || req.Port > 65535 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(RegisterResponse{
			Status:  "error",
			Message: "invalid port",
		})
		return
	}

	internalID := toInternalID(req.ID)

	sm.mu.Lock()
	if _, exists := sm.clients[internalID]; exists {
		sm.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(RegisterResponse{
			Status:  "error",
			Message: "subdomain already in use",
		})
		return
	}

	client := &Client{
		ID:            internalID,
		Port:          req.Port,
		Subdomain:     req.ID,
		LastHeartbeat: time.Now(),
	}
	sm.clients[internalID] = client
	sm.mu.Unlock()

	log.Printf("Client registered: %s -> port %d", client.Subdomain, client.Port)
	sm.generateConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(RegisterResponse{
		Status: "registered",
		URL:    client.Subdomain + ".localhost",
	})
}

func (sm *ServerManager) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "missing id parameter",
		})
		return
	}

	internalID := toInternalID(id)

	sm.mu.Lock()
	client, exists := sm.clients[internalID]
	if !exists {
		sm.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "client not found",
		})
		return
	}

	client.LastHeartbeat = time.Now()
	sm.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (sm *ServerManager) handleUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "missing id parameter",
		})
		return
	}

	internalID := toInternalID(id)

	sm.mu.Lock()
	_, exists := sm.clients[internalID]
	if !exists {
		sm.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "error",
			"message": "client not found",
		})
		return
	}

	delete(sm.clients, internalID)
	sm.mu.Unlock()

	log.Printf("Client unregistered: %s", id)
	sm.generateConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "unregistered",
	})
}

func (sm *ServerManager) checkHeartbeats() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		expired := []string{}

		for id, client := range sm.clients {
			if now.Sub(client.LastHeartbeat) > sm.heartbeatTimeout {
				expired = append(expired, id)
			}
		}

		for _, id := range expired {
			delete(sm.clients, id)
			log.Printf("Client expired (no heartbeat): %s", id)
		}

		sm.mu.Unlock()

		if len(expired) > 0 {
			sm.generateConfig()
		}
	}
}

func (sm *ServerManager) generateConfig() {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	config := TraefikConfig{}
	config.HTTP.Routers = make(map[string]Router)
	config.HTTP.Services = make(map[string]Service)

	for subdomain, client := range sm.clients {
		routerName := "sub-" + subdomain
		serviceName := "local-" + subdomain

		config.HTTP.Routers[routerName] = Router{
			EntryPoints: []string{"web"},
			Rule:        "Host(`" + client.Subdomain + ".localhost`)",
			Service:     serviceName,
		}

		config.HTTP.Services[serviceName] = Service{
			LoadBalancer: LoadBalancer{
				Servers: []Server{
					{URL: fmt.Sprintf("http://host.docker.internal:%d", client.Port)},
				},
			},
		}
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		log.Printf("Failed to marshal config: %v", err)
		return
	}

	configPath := sm.configDir + "/dynamic.yml"
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		log.Printf("Failed to write config: %v", err)
		return
	}

	log.Printf("Generated Traefik config with %d routes", len(sm.clients))
}

func (sm *ServerManager) getStatus(w http.ResponseWriter, r *http.Request) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	response := map[string]any{
		"status":  "ok",
		"clients": len(sm.clients),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (sm *ServerManager) getClients(w http.ResponseWriter, r *http.Request) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	clients := make([]map[string]any, 0, len(sm.clients))
	for _, client := range sm.clients {
		clients = append(clients, map[string]any{
			"id":             client.ID,
			"domain":         client.Subdomain + ".localhost",
			"port":           client.Port,
			"last_heartbeat": client.LastHeartbeat.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"clients": clients,
	})
}

func main() {
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "/config"
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Fatalf("Failed to create config directory: %v", err)
	}

	heartbeatTimeout := 30 * time.Second
	if timeout := os.Getenv("HEARTBEAT_TIMEOUT"); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			heartbeatTimeout = d
		}
	}

	manager := NewServerManager(configDir, heartbeatTimeout)

	http.HandleFunc("/register", manager.handleRegister)
	http.HandleFunc("/heartbeat", manager.handleHeartbeat)
	http.HandleFunc("/unregister", manager.handleUnregister)
	http.HandleFunc("/status", manager.getStatus)
	http.HandleFunc("/clients", manager.getClients)

	go manager.checkHeartbeats()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		log.Printf("Server starting on :%s (heartbeat timeout: %v)", port, heartbeatTimeout)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}
