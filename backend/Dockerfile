FROM golang:1.22.5-alpine

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY config.yaml /app/config.yaml

RUN go build -o main .

EXPOSE 40900

# CMD ["./main"]