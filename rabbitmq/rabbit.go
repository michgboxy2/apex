package rabbitqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type EmailJob struct {
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	RetryCount int
}

type Queue struct {
	conn       *amqp.Connection
	ch         *amqp.Channel
	name       string
	deadLetter []EmailJob
	mu         sync.Mutex
}

var (
	JobQueue   *Queue
	emailRegex = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")
)

func NewQueue(name, user, pass, host string) (*Queue, error) {
	conn, err := amqp.Dial(fmt.Sprintf("amqp://%s:%s@%s:5672/", user, pass, host))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	q, err := ch.QueueDeclare(
		name,  // name
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)

	if err != nil {
		return nil, fmt.Errorf("failed to declare a queue: %w", err)
	}

	return &Queue{
		conn: conn,
		ch:   ch,
		name: q.Name,
	}, nil

}

func validateEmailRequest(req EmailJob) error {
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

func (q *Queue) Enqueue(job EmailJob) error {
	body, err := json.Marshal(job)

	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println(q.name)

	err = q.ch.PublishWithContext(ctx,
		"",
		q.name,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		})

	if err != nil {
		return fmt.Errorf("failed to publish a message: %w", err)
	}

	return nil
}

func (q *Queue) Close() {
	q.ch.Close()
	q.conn.Close()
	log.Println("RabbitMQ connection closed.")
}

func (q *Queue) AddDeadLetter(job EmailJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deadLetter = append(q.deadLetter, job)
	log.Printf("Job for %s failed and moved to Dead Letter Queue", job.To)
}

func (q *Queue) PendingJobs() int {
	return 0
}

type Workers struct {
	count int
	wg    *sync.WaitGroup
}

func NewWorkers(count int, wg *sync.WaitGroup) *Workers {
	return &Workers{count: count, wg: wg}
}

func (w *Workers) StartWorkers(queue *Queue) {
	log.Printf("starting %d workers", w.count)
	for i := 1; i <= w.count; i++ {
		w.wg.Add(1)
		go w.processJobs(i, queue)
	}
}

func (w *Workers) processJobs(id int, queue *Queue) {
	defer w.wg.Done()

	log.Printf("Worker %d started", id)

	msgs, err := queue.ch.Consume(
		queue.name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("Failed to register a consumer: %v", err)
		return
	}

	for d := range msgs {
		var job EmailJob

		err = json.Unmarshal(d.Body, &job)

		if err != nil {
			log.Printf("Failed to unmarshal job: %v", err)
			d.Nack(false, false)
			continue
		}
		processJob(job, queue)
		d.Ack(false)
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

func SendEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var job EmailJob

	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, "Invalid Request", http.StatusBadRequest)
		return
	}

	if err := validateEmailRequest(job); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	if err := JobQueue.Enqueue(job); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "Email job enqueued successfully"})
	log.Printf("New email job enqueued for %s. Pending jobs in queue: %d", job.To, JobQueue.PendingJobs())

}
