package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

// ============================================
// Configuration from environment variables
// ============================================
type Config struct {
	RedisHost    string
	RedisPort    string
	PostgresHost string
	PostgresPort string
	PostgresUser string
	PostgresPass string
	PostgresDB   string
	HealthPort   string
}

func loadConfig() Config {
	return Config{
		RedisHost:    os.Getenv("REDIS_HOST"),
		RedisPort:    os.Getenv("REDIS_PORT"),
		PostgresHost: os.Getenv("POSTGRES_HOST"),
		PostgresPort: os.Getenv("POSTGRES_PORT"),
		PostgresUser: os.Getenv("POSTGRES_USER"),
		PostgresPass: os.Getenv("POSTGRES_PASSWORD"),
		PostgresDB:   os.Getenv("POSTGRES_DB"),
		HealthPort:   os.Getenv("WORKER_HEALTH_PORT"),
	}
}

// ============================================
// Vote data structure
// ============================================
type Vote struct {
	VoterID string `json:"voter_id"`
	Vote    string `json:"vote"`
}

// ============================================
// Global connections for health checks
// ============================================
var (
	globalRedis *redis.Client
	globalDB    *sql.DB
)

func main() {
	cfg := loadConfig()

	log.Println("Worker starting...")
	log.Printf("Redis: %s:%s", cfg.RedisHost, cfg.RedisPort)
	log.Printf("PostgreSQL: %s:%s/%s", cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresDB)

	// Start health check HTTP server in background
	go startHealthServer(cfg.HealthPort)

	// Connect to PostgreSQL with retry
	db := connectDB(cfg)
	globalDB = db
	defer db.Close()

	// Create votes table if not exists
	createTable(db)

	// Connect to Redis with retry
	rdb := connectRedis(cfg)
	globalRedis = rdb
	defer rdb.Close()

	log.Println("Worker is running, waiting for votes...")

	ctx := context.Background()

	// Main processing loop
	for {
		// Check and reconnect Redis if needed
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Println("Redis connection lost, reconnecting...")
			rdb = connectRedis(cfg)
			globalRedis = rdb
		}

		// Check and reconnect DB if needed
		if err := db.Ping(); err != nil {
			log.Println("DB connection lost, reconnecting...")
			db = connectDB(cfg)
			globalDB = db
		}

		// Pop vote from Redis queue
		result, err := rdb.LPop(ctx, "votes").Result()
		if err == redis.Nil {
			// No votes in queue, keep alive DB connection
			db.Ping()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err != nil {
			log.Printf("Error reading from Redis: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// Parse vote
		var vote Vote
		if err := json.Unmarshal([]byte(result), &vote); err != nil {
			log.Printf("Error parsing vote JSON: %v", err)
			continue
		}

		log.Printf("Processing vote for '%s' by '%s'", vote.Vote, vote.VoterID)

		// Update vote in PostgreSQL
		updateVote(db, vote.VoterID, vote.Vote)
	}
}

// ============================================
// Database operations
// ============================================
func connectDB(cfg Config) *sql.DB {
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.PostgresHost, cfg.PostgresPort, cfg.PostgresUser, cfg.PostgresPass, cfg.PostgresDB,
	)

	for {
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			log.Printf("Waiting for db: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if err = db.Ping(); err != nil {
			log.Printf("Waiting for db: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		log.Println("Connected to db")
		return db
	}
}

func createTable(db *sql.DB) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS votes (
			id VARCHAR(255) NOT NULL UNIQUE,
			vote VARCHAR(255) NOT NULL
		)
	`)
	if err != nil {
		log.Fatalf("Error creating votes table: %v", err)
	}
	log.Println("Votes table ready")
}

func updateVote(db *sql.DB, voterID, vote string) {
	// Try INSERT first, then UPDATE on conflict
	_, err := db.Exec(
		"INSERT INTO votes (id, vote) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET vote = $2",
		voterID, vote,
	)
	if err != nil {
		log.Printf("Error updating vote: %v", err)
	}
}

// ============================================
// Redis connection
// ============================================
func connectRedis(cfg Config) *redis.Client {
	addr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
	ctx := context.Background()

	for {
		rdb := redis.NewClient(&redis.Options{
			Addr:     addr,
			DB:       0,
			PoolSize: 10,
		})

		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("Waiting for redis: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		log.Println("Connected to redis")
		return rdb
	}
}

// ============================================
// Health check HTTP server
// ============================================
func startHealthServer(port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)

	addr := ":" + port
	log.Printf("Health check server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("Health server error: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	response := map[string]string{
		"status": "ok",
	}
	statusCode := http.StatusOK

	// Check Redis
	if globalRedis != nil {
		if err := globalRedis.Ping(ctx).Err(); err != nil {
			response["redis"] = "disconnected"
			response["status"] = "error"
			statusCode = http.StatusServiceUnavailable
		} else {
			response["redis"] = "connected"
		}
	} else {
		response["redis"] = "not_initialized"
		response["status"] = "error"
		statusCode = http.StatusServiceUnavailable
	}

	// Check PostgreSQL
	if globalDB != nil {
		if err := globalDB.Ping(); err != nil {
			response["postgres"] = "disconnected"
			response["status"] = "error"
			statusCode = http.StatusServiceUnavailable
		} else {
			response["postgres"] = "connected"
		}
	} else {
		response["postgres"] = "not_initialized"
		response["status"] = "error"
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}
