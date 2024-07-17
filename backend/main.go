package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"gopkg.in/yaml.v2"
)

// Config struct to hold our configuration
type Config struct {
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
		Mode string `yaml:"mode"`
	} `yaml:"server"`
	TLS struct {
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
	} `yaml:"tls"`
	Docker struct {
		ImageName     string `yaml:"image_name"`
		MaxContainers int    `yaml:"max_containers"`
	} `yaml:"docker"`
	Paths struct {
		CodeDir           string `yaml:"code_dir"`
		PersistentCodeDir string `yaml:"persistent_code_dir"`
		FrontendDir       string `yaml:"frontend_dir"`
	} `yaml:"paths"`
	Security struct {
		AllowedIPs []string `yaml:"allowed_ips"`
	} `yaml:"security"`
}

type Request struct {
	Code string `json:"code"`
}

type SaveCodeRequest struct {
	Code   string `json:"code"`
	NoteID string `json:"noteId"`
}

type SaveCodeResponse struct {
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type GetCodeRequest struct {
	NoteID string `json:"noteId"`
}

type GetCodeResponse struct {
	Code     string        `json:"code"`
	Success  bool          `json:"success"`
	Message  string        `json:"message"`
	Versions []CodeVersion `json:"versions"`
}

type Response struct {
	Output string `json:"output"`
}

type CodeVersion struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

var (
	containerPool chan string
	cli           *client.Client
	mu            sync.Mutex
	config        Config
)

func loadConfig() error {
	f, err := os.Open("config.yaml")
	if err != nil {
		return err
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&config)
	if err != nil {
		return err
	}

	// Initialize containerPool after loading config
	containerPool = make(chan string, config.Docker.MaxContainers)

	return nil
}

func init() {
	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	var err error
	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

	if err := os.MkdirAll(config.Paths.CodeDir, 0755); err != nil {
		log.Fatalf("Failed to create code directory: %v", err)
	}

	if err := os.MkdirAll(config.Paths.PersistentCodeDir, 0755); err != nil {
		log.Fatalf("Failed to create persistent code directory: %v", err)
	}

	for i := 0; i < config.Docker.MaxContainers; i++ {
		containerID, err := createContainer()
		if err != nil {
			log.Fatalf("Failed to create container: %v", err)
		}
		containerPool <- containerID
	}
}

func createContainer() (string, error) {
	ctx := context.Background()
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: config.Docker.ImageName,
		Cmd:   []string{"tail", "-f", "/dev/null"},
		Tty:   true,
	}, &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: config.Paths.CodeDir,
				Target: "/code",
			},
			{
				Type:   mount.TypeVolume,
				Source: "persistent_code",
				Target: config.Paths.PersistentCodeDir,
			},
		},
	}, nil, nil, "")
	if err != nil {
		return "", err
	}

	if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return "", err
	}

	return resp.ID, nil
}

func getContainer() (string, error) {
	select {
	case id := <-containerPool:
		return id, nil
	default:
		return <-containerPool, nil
	}
}

func releaseContainer(id string) {
	containerPool <- id
}

func cleanDockerOutput(rawOutput []byte) string {
	if len(rawOutput) == 0 {
		log.Println("Warning: Empty raw output")
		return ""
	}

	var cleanedOutput []byte
	for i := 0; i < len(rawOutput); {
		if i+8 > len(rawOutput) {
			log.Printf("Warning: Unexpected end of output at index %d\n", i)
			break
		}

		frameType := rawOutput[i]
		frameSize := int(binary.BigEndian.Uint32(rawOutput[i+4 : i+8]))

		if frameSize == 0 {
			i += 8
			continue
		}

		if i+8+frameSize > len(rawOutput) {
			log.Printf("Warning: Frame size %d exceeds remaining data at index %d\n", frameSize, i)
			break
		}

		if frameType == 1 || frameType == 2 {
			cleanedOutput = append(cleanedOutput, rawOutput[i+8:i+8+frameSize]...)
		}

		i += 8 + frameSize
	}

	return string(cleanedOutput)
}

func enableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func runCode(w http.ResponseWriter, r *http.Request) {
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	mu.Lock()
	fileName := filepath.Join(config.Paths.CodeDir, fmt.Sprintf("main_%d.go", time.Now().UnixNano()))
	if err := os.WriteFile(fileName, []byte(req.Code), 0644); err != nil {
		mu.Unlock()
		log.Printf("Failed to write code file: %v\n", err)
		http.Error(w, "Failed to write code file", http.StatusInternalServerError)
		return
	}
	mu.Unlock()

	containerID, err := getContainer()
	if err != nil {
		log.Printf("Failed to get container: %v\n", err)
		http.Error(w, "Failed to get container", http.StatusInternalServerError)
		return
	}
	defer releaseContainer(containerID)

	ctx := context.Background()
	execResp, err := cli.ContainerExecCreate(ctx, containerID, types.ExecConfig{
		Cmd:          []string{"go", "run", filepath.Join("/code", filepath.Base(fileName))},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		log.Printf("Error creating exec: %v\n", err)
		http.Error(w, "Failed to create exec", http.StatusInternalServerError)
		return
	}

	resp, err := cli.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
	if err != nil {
		log.Printf("Error attaching exec: %v\n", err)
		http.Error(w, "Failed to attach exec", http.StatusInternalServerError)
		return
	}
	defer resp.Close()

	output, err := io.ReadAll(resp.Reader)
	if err != nil {
		log.Printf("Error reading exec output: %v\n", err)
		http.Error(w, "Failed to read exec output", http.StatusInternalServerError)
		return
	}

	log.Printf("Raw output length: %d\n", len(output))

	os.Remove(fileName)

	cleanedOutput := cleanDockerOutput(output)
	log.Printf("Cleaned output length: %d\n", len(cleanedOutput))
	log.Printf("Cleaned output: %s\n", cleanedOutput)

	response := Response{Output: cleanedOutput}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func saveCode(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIP(r)
	if !isAllowedIP(clientIP) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req SaveCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	noteDir := filepath.Join(config.Paths.PersistentCodeDir, req.NoteID)
	if err := os.MkdirAll(noteDir, 0755); err != nil {
		log.Printf("Failed to create note directory: %v\n", err)
		http.Error(w, "Failed to save code", http.StatusInternalServerError)
		return
	}

	timestamp := time.Now()
	fileName := filepath.Join(noteDir, fmt.Sprintf("%d.go", timestamp.UnixNano()))
	if err := os.WriteFile(fileName, []byte(req.Code), 0644); err != nil {
		log.Printf("Failed to save code: %v\n", err)
		http.Error(w, "Failed to save code", http.StatusInternalServerError)
		return
	}

	cleanupOldVersions(noteDir)

	response := SaveCodeResponse{
		Success:   true,
		Message:   "Code saved successfully",
		Timestamp: timestamp,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getCode(w http.ResponseWriter, r *http.Request) {
	var req GetCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	noteDir := filepath.Join(config.Paths.PersistentCodeDir, req.NoteID)
	versions, err := getCodeVersions(noteDir)
	if err != nil {
		log.Printf("Failed to get code versions: %v\n", err)
		http.Error(w, "Failed to get code", http.StatusInternalServerError)
		return
	}

	var latestCode string
	if len(versions) > 0 {
		latestCode = versions[0].Content
	}

	response := GetCodeResponse{
		Code:     latestCode,
		Success:  true,
		Message:  "Code retrieved successfully",
		Versions: versions,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func getCodeVersions(noteDir string) ([]CodeVersion, error) {
	files, err := os.ReadDir(noteDir)
	if err != nil {
		return nil, err
	}

	versions := make([]CodeVersion, 0, len(files))
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".go" {
			content, err := os.ReadFile(filepath.Join(noteDir, file.Name()))
			if err != nil {
				return nil, err
			}
			timestamp, err := parseTimestamp(file.Name())
			if err != nil {
				return nil, err
			}
			versions = append(versions, CodeVersion{
				Content:   string(content),
				Timestamp: timestamp,
			})
		}
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Timestamp.After(versions[j].Timestamp)
	})

	return versions, nil
}

func parseTimestamp(fileName string) (time.Time, error) {
	base := filepath.Base(fileName)
	ext := filepath.Ext(base)
	nameWithoutExt := base[:len(base)-len(ext)]
	nanoSeconds, err := strconv.ParseInt(nameWithoutExt, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, nanoSeconds), nil
}

func cleanupOldVersions(noteDir string) {
	versions, err := getCodeVersions(noteDir)
	if err != nil {
		log.Printf("Failed to get code versions for cleanup: %v\n", err)
		return
	}

	if len(versions) <= config.Docker.MaxContainers {
		return
	}

	for _, version := range versions[config.Docker.MaxContainers:] {
		fileName := filepath.Join(noteDir, fmt.Sprintf("%d.go", version.Timestamp.UnixNano()))
		if err := os.Remove(fileName); err != nil {
			log.Printf("Failed to remove old version: %v\n", err)
		}
	}
}

func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	return ip
}

func isAllowedIP(ip string) bool {
	for _, allowedIP := range config.Security.AllowedIPs {
		if strings.Contains(ip, allowedIP) {
			return true
		}
	}
	return false
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	if err := os.MkdirAll(config.Paths.PersistentCodeDir, 0755); err != nil {
		log.Fatalf("Failed to create persistent code directory: %v", err)
	}

	fs := http.FileServer(http.Dir(config.Paths.FrontendDir))
	http.Handle("/", fs)
	http.HandleFunc("/run", enableCORS(runCode))
	http.HandleFunc("/saveCode", enableCORS(saveCode))
	http.HandleFunc("/getCode", enableCORS(getCode))

	addr := fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port)

	switch config.Server.Mode {
	case "local":
		log.Printf("Server is running in local mode on http://%s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("Failed to start server in local mode: %v", err)
		}
	case "production":
		log.Printf("Server is running in production mode with TLS on https://%s", addr)
		if err := http.ListenAndServeTLS(addr, config.TLS.CertFile, config.TLS.KeyFile, nil); err != nil {
			log.Fatalf("Failed to start server in production mode with TLS: %v", err)
		}
	case "production-no-tls":
		log.Printf("Server is running in production mode without TLS on http://%s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatalf("Failed to start server in production mode without TLS: %v", err)
		}
	default:
		log.Fatalf("Invalid server mode: %s", config.Server.Mode)
	}
}
