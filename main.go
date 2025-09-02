package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	channelQueue "apex/channelq"
	rabbitQueue "apex/rabbitmq"

	"github.com/joho/godotenv"
)

var (
	wg sync.WaitGroup
)

func init() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}
}

func main() {
	workersNumStr := os.Getenv("WORKERS_COUNT")
	queueSizeStr := os.Getenv("QUEUE_SIZE")
	AMQPUser := os.Getenv("RABBITMQ_USER")
	AMQPPass := os.Getenv("RABBITMQ_PASS")
	AMQPHost := os.Getenv("RABBITMQ_HOST")
	AMQPQueueName := os.Getenv("AMQP_QUEUE_NAME")
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

	jobQueue := channelQueue.NewQueue(queueSize)

	if jobQueue == nil {
		log.Fatal("Failed to initialize job queue")
	}

	rabbitJobQueue, err := rabbitQueue.NewQueue(AMQPQueueName, AMQPUser, AMQPPass, AMQPHost)

	if err != nil {
		log.Fatalf("Failed to initialize rabbit job queue: %v", err)
	}

	rabbitQueue.JobQueue = rabbitJobQueue

	channelQueue.JobQueue = jobQueue

	workers := channelQueue.NewWorkers(workersNum, &wg)
	workers.StartWorkers(jobQueue)

	// start RabbitMQ worker
	rabbitMQWorkers := rabbitQueue.NewWorkers(workersNum, &wg)
	rabbitMQWorkers.StartWorkers(rabbitJobQueue)

	http.HandleFunc("/send-email", channelQueue.SendEmailHandler)

	http.HandleFunc("/send-email2", rabbitQueue.SendEmailHandler)

	server := &http.Server{Addr: fmt.Sprintf(":%s", port)}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on port :%s", port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server ListenAndServe error: %v", err)
		}
	}()

	<-quit
	log.Println("shutdown initiated.")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("server shutdown error: %v", err)
	}

	channelQueue.JobQueue.Close()
	rabbitQueue.JobQueue.Close()

	log.Println("Waiting for all workers to finish.")
	wg.Wait()
	log.Println("All workers have finished. Exiting.")

}
