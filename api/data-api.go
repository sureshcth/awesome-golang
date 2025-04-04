package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

// Data structure for our API.
type Data struct {
	ID        int    `json:"id"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// In-memory data store (for simplicity; replace with a database in a real app).
var (
	dataStore = make(map[int]Data)
	dataMutex sync.RWMutex
	nextID    = 1
)

// Handler for creating new data.
func createData(w http.ResponseWriter, r *http.Request) {
	var newData Data
	if err := json.NewDecoder(r.Body).Decode(&newData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dataMutex.Lock()
	newData.ID = nextID
	newData.Timestamp = time.Now().Unix()
	dataStore[nextID] = newData
	nextID++
	dataMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newData)
}

// Handler for retrieving data by ID.
func getData(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	dataMutex.RLock()
	data, ok := dataStore[int(mustAtoi(id))]
	dataMutex.RUnlock()

	if !ok {
		http.Error(w, "Data not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// Handler for updating data by ID.
func updateData(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var updatedData Data
	if err := json.NewDecoder(r.Body).Decode(&updatedData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dataMutex.Lock()
	_, ok := dataStore[int(mustAtoi(id))]
	if !ok {
		dataMutex.Unlock()
		http.Error(w, "Data not found", http.StatusNotFound)
		return
	}

	updatedData.ID = int(mustAtoi(id))
	updatedData.Timestamp = time.Now().Unix()
	dataStore[int(mustAtoi(id))] = updatedData
	dataMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updatedData)
}

// Handler for deleting data by ID.
func deleteData(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	dataMutex.Lock()
	_, ok := dataStore[int(mustAtoi(id))]
	if !ok {
		dataMutex.Unlock()
		http.Error(w, "Data not found", http.StatusNotFound)
		return
	}

	delete(dataStore, int(mustAtoi(id)))
	dataMutex.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// Helper function to convert string to int.
func mustAtoi(s string) int {
	var i int
	i, err := fmt.Sscanf(s, "%d", &i)
	if err != nil || i == 0 {
		return 0
	}
	return i
}

func main() {
	r := mux.NewRouter()

	r.HandleFunc("/data", createData).Methods("POST")
	r.HandleFunc("/data/{id}", getData).Methods("GET")
	r.HandleFunc("/data/{id}", updateData).Methods("PUT")
	r.HandleFunc("/data/{id}", deleteData).Methods("DELETE")

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}
