# Apex Email MicroService

This service is a simple email job processor that uses RabbitMQ and go Channels message brokers.

It accepts email jobs, enqueues them into RabbitMQ or Go Channel, and processes them asynchronously with worker goroutines.

---

- HTTP API to enqueue email jobs on PORT 5000
- RabbitMQ integration for job queuing
- Worker pool for concurrent job processing
- Graceful shutdown (Ctrl+C support)

##Running the Service

1. Clone this repository:

   git https://github.com/michgboxy2/apex.git

   cd apex

2. Start RabbitMQ and Email service

RUN docker compose up --build

url: http://localhost:5000/send-email
method: POST
payload: {
"to": "user@example.com",
"subject": "Welcome!",
"body": "Thanks for signing up."
}
