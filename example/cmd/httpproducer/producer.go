package main

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"github.com/arslanovdi/otelsarama"
	"github.com/arslanovdi/otelsarama/example/tracer"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	servicename = "otelsaramaExample"
	instance    = servicename + "-1"
	kafkaTopic  = "test_topic"
	timer       = 5 * time.Second
	loglevel    = slog.LevelDebug
)

var brokers = []string{"localhost:29092", "localhost:29093", "localhost:29094"}

type kafkaproducer struct {
	kafka sarama.SyncProducer
}

func (p *kafkaproducer) initialize() {
	kafkaConfig := sarama.NewConfig()
	oi := otelsarama.NewOTelInterceptor(instance)
	kafkaConfig.Producer.Interceptors = []sarama.ProducerInterceptor{oi}
	kafkaConfig.Producer.RequiredAcks = sarama.WaitForAll // WaitForAll - Ждем подтверждения всех синхронизированный реплик
	kafkaConfig.Producer.Retry.Max = 3                    // максимальное количество попыток передачи сообщения
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.ChannelBufferSize = 512

	var err error
	p.kafka, err = sarama.NewSyncProducer(brokers, kafkaConfig)

	if err != nil {
		slog.Warn("Failed to create kafka producer", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func main() {

	slog.SetDefault(
		slog.New(slog.NewJSONHandler(
			os.Stderr,
			&slog.HandlerOptions{
				Level: loglevel})))

	OTeltracer, err := tracer.NewTracer(context.TODO(), servicename)
	if err != nil {
		panic(err)
	}
	defer func() {
		err := OTeltracer.Shutdown(context.TODO())
		if err != nil {
			slog.Warn("tracer shutdown error", slog.String("error", err.Error()))
		}
	}()

	producer := kafkaproducer{}
	producer.initialize()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		go func() {
			slog.Info("send message to kafka", slog.String("id", id))
			msg := &sarama.ProducerMessage{
				Topic: kafkaTopic,
				Key:   sarama.StringEncoder("test key"),
				Value: sarama.StringEncoder("test value " + id),
			}

			msg = otelsarama.SetRootSpanContext(r.Context(), msg)
			_, _, err := producer.kafka.SendMessage(msg)

			for err != nil { // retry. Sarama enable circuit breaker after Producer.Retry.Max retries
				_, _, err = producer.kafka.SendMessage(msg)
			}

			slog.Debug("Send message to kafka", slog.Any("message", msg))

		}()
	})

	server := &http.Server{
		Addr:              "localhost:8081",
		Handler:           otelhttp.NewHandler(mux, "http-server"), //mux,
		ReadHeaderTimeout: time.Second * 5,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Failed to listen and serve", slog.String("error", err.Error()))
		}
		slog.Info("http server stopped")
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(timer)

	i := 0

loop:
	for {
		select {
		case <-ticker.C:
			client := http.Client{Timeout: time.Second * 5}
			_, _ = client.Post("http://localhost:8081/"+strconv.Itoa(i), "application/json", nil)
			i++
		case <-stop:
			break loop
		}
	}

	ticker.Stop()

	err = producer.kafka.Close()

	if err != nil {
		slog.Warn("Failed to close kafka producer", slog.String("error", err.Error()))
	}

	err = OTeltracer.Shutdown(context.TODO())
	if err != nil {
		slog.Warn("tracer shutdown error", slog.String("error", err.Error()))
	}
	slog.Info("shutdown")
}
