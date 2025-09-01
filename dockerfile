FROM golang:1.23-alpine
WORKDIR /app
COPY . .
RUN go mod tidy
RUN go build -o main .


FROM alpine:latest
WORKDIR /app
COPY --from=0 /app/main .
EXPOSE 5000

CMD ["./main"]