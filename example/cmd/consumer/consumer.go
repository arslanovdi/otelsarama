package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/IBM/sarama"
	"github.com/arslanovdi/otelsarama"
	"github.com/arslanovdi/otelsarama/example/tracer"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	servicename = "otelsaramaConsumerExample"
	instance    = servicename + "-1"
)

var brokers = []string{"localhost:29092", "localhost:29093", "localhost:29094"}

const kafkaTopic = "test_topic"

const kafkaGroup = "test group"

const loglevel = slog.LevelDebug

type Consumer struct{}

func (c Consumer) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (c Consumer) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }
func (c Consumer) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		// process the message
		fmt.Printf("Message topic:%q partition:%d offset:%d key:%s value:%s\n", msg.Topic, msg.Partition, msg.Offset, msg.Key, msg.Value)
		// after processing the message, mark the offset
		sess.MarkMessage(msg, "")

		otherService(otelsarama.Context(msg))
	}
	return nil
}

func otherService(ctx context.Context) {
	t := otel.GetTracerProvider().Tracer("other service")
	_, span := t.Start(
		ctx,
		"span name in other service",
		trace.WithAttributes(
			attribute.String("messaging.other.service.name", "other service")),
	)
	span.End()
}

func main() {
	slog.SetDefault(
		slog.New(slog.NewJSONHandler(
			os.Stderr,
			&slog.HandlerOptions{
				Level: loglevel,
			})))

	oTeltracer, err := tracer.NewTracer(context.TODO(), servicename)
	if err != nil {
		panic(err)
	}
	defer func() {
		err1 := oTeltracer.Shutdown(context.TODO())
		if err1 != nil {
			slog.Warn("tracer shutdown error", slog.String("error", err1.Error()))
		}
	}()

	kafkaConfig := sarama.NewConfig()
	oi := otelsarama.NewOTelInterceptor(instance)
	kafkaConfig.Consumer.Interceptors = []sarama.ConsumerInterceptor{oi}
	kafkaConfig.Consumer.Offsets.AutoCommit.Enable = true
	kafkaConfig.Consumer.Offsets.AutoCommit.Interval = 1 * time.Second

	consumerGroup, err := sarama.NewConsumerGroup(brokers, kafkaGroup, kafkaConfig)
	if err != nil {
		slog.Warn("Failed to create kafka consumer group", slog.String("error", err.Error()))
		os.Exit(1)
	}

	consumer := Consumer{}

	for {
		err := consumerGroup.Consume(context.TODO(), []string{kafkaTopic}, consumer)
		if err != nil {
			slog.Warn("Failed to consume kafka topic", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}
}
