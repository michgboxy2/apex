package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type EmailJob struct {
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	RetryCount int
}

type Queue struct {
	jobs       chan EmailJob
	deadLetter []EmailJob
	mu         sync.Mutex
	closed     bool
}

type Workers struct {
	count int
}

var (
	jobQueue   *Queue
	wg         sync.WaitGroup
	emailRegex = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
)

func ValidateEmailRequest(req EmailJob) error {
	if req.To == "" {
		return errors.New("'to' field is required")
	}
	if req.Subject == "" {
		return errors.New("'subject' field is required")
	}
	if req.Body == "" {
		return errors.New("'body' field is required")
	}
	if !emailRegex.MatchString(req.To) {
		return errors.New("'to' field must be a valid email address")
	}
	return nil
}

func NewQueue(size int) *Queue {
	return &Queue{
		jobs: make(chan EmailJob, size),
	}
}

func (q *Queue) Enqueue(job EmailJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return errors.New("queue is closed")
	}

	select {
	case q.jobs <- job:
		return nil
	default:
		return errors.New("queue is full")
	}
}

func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.closed {
		close(q.jobs)
		q.closed = true
	}
}

func (q *Queue) AddDeadLetter(job EmailJob) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.deadLetter = append(q.deadLetter, job)

	log.Printf("Job for %s failed and moved to Dead Letter Queue", job.To)
}

func (q *Queue) PendingJobs() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.jobs)
}

func NewWorkers(count int) *Workers {
	return &Workers{count: count}
}

func (w *Workers) StartWorkers(queue *Queue) {
	log.Printf("starting %d workers", w.count)

	for i := 1; i <= w.count; i++ {
		wg.Add(1)
		go w.processJobs(i, queue)
	}
}

func (w *Workers) processJobs(id int, queue *Queue) {
	defer wg.Done()
	log.Printf("Worker %d started", id)

	for job := range queue.jobs {
		processJob(job, queue)
	}

	log.Printf("Worker %d stopped", id)
}

func processJob(job EmailJob, queue *Queue) {
	log.Printf("worker processing job for %s (subject: %s).", job.To, job.Subject)

	time.Sleep(1 * time.Second)

	if job.RetryCount >= 3 {
		queue.AddDeadLetter(job)
		return
	}

	log.Printf("successfully sent email to %s.", job.To)
}

func sendEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var job EmailJob

	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, "Invalid Request", http.StatusBadRequest)
		return
	}

	if err := ValidateEmailRequest(job); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	if err := jobQueue.Enqueue(job); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "Email job enqueued successfully"})
	log.Printf("New email job enqueued for %s. Pending jobs in queue: %d", job.To, jobQueue.PendingJobs())

}

func main() {
	workersNumStr := os.Getenv("WORKERS_COUNT")
	queueSizeStr := os.Getenv("QUEUE_SIZE")
	port := os.Getenv("PORT")

	queueSize, err := strconv.Atoi(queueSizeStr)
	if err != nil {
		log.Fatalf("Invalid QUEUE_SIZE: %v", err)
	}

	workersNum, err := strconv.Atoi(workersNumStr)
	if err != nil {
		log.Fatalf("Invalid WORKERS_COUNT: %v", err)
	}

	if port == "" {
		port = "5000"
	}

	jobQueue = NewQueue(queueSize)

	if jobQueue == nil {
		log.Fatal("Failed to initialize job queue")
	}

	workers := NewWorkers(workersNum)
	workers.StartWorkers(jobQueue)

	http.HandleFunc("/send-email", sendEmailHandler)

	server := &http.Server{Addr: fmt.Sprintf(":%s", port)}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("shutdown initiated.")

		if err := server.Shutdown(nil); err != nil {
			log.Fatalf("server shutdown error: %v", err)
		}

		jobQueue.Close()
		log.Println("Waiting for all workers to finish.")
		wg.Wait()
		log.Println("All workers have finished. Exiting.")
	}()

	log.Printf("Server listening on port :%s", port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server ListenAndServe error: %v", err)
	}

}
