//go:build integration

package otelsarama

import (
	"context"
	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

var brokers = []string{"127.0.0.1:29092"}
var duration = 1 * time.Second // время ожидания отправки сообщения в буфер провайдером OpenTelemetry

const someheader = "SomeHeaderName"

func TestOTelInterceptor_OnSend(t *testing.T) {
	buf.Reset()
	// Connect to kafka
	kafkaConfig := sarama.NewConfig()
	oi := NewOTelInterceptor("test service")
	kafkaConfig.Producer.Interceptors = []sarama.ProducerInterceptor{oi}
	kafkaConfig.Producer.RequiredAcks = sarama.WaitForAll // WaitForAll - Ждем подтверждения всех синхронизированный реплик
	kafkaConfig.Producer.Retry.Max = 3                    // максимальное количество попыток передачи сообщения
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.ChannelBufferSize = 512

	kafka, err := sarama.NewSyncProducer(brokers, kafkaConfig)
	if err != nil {
		assert.Fail(t, "Failed to create kafka producer", err)
	}

	// create root span
	ctx, span := tracer.Start(context.Background(), "root span name in test") // сгенерировали span
	traceId := span.SpanContext().TraceID().String()

	// create message
	msg := &sarama.ProducerMessage{
		Topic: "test_topic",
		Key:   sarama.StringEncoder("test key"),
		Value: sarama.StringEncoder("test value"),
	}

	msg = SetRootSpanContext(ctx, msg)
	_, _, err = kafka.SendMessage(msg)

	time.Sleep(duration) // waiting for OpenTelemetry provider send tracing

	// check tracing
	found := 0
	for {
		line, err := buf.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			assert.Fail(t, "Failed to read line", err)
			break
		}

		if strings.Contains(line, traceId) { // нашли root traceid
			found++
		}
	}
	assert.Equal(t, 2, found) // должно быть 2 вхождения от трассировки producer и consumer

	buf.Reset()
}

func TestOTelInterceptor_OnConsume(t *testing.T) {
	buf.Reset()
	// create root span for producer
	ctx, span := tracer.Start(context.Background(), "root span name in test") // сгенерировали span
	traceId := span.SpanContext().TraceID().String()

	// Создание консюмера Kafka
	consumerConfig := sarama.NewConfig()
	oiconsume := NewOTelInterceptor("test service")
	consumerConfig.Consumer.Interceptors = []sarama.ConsumerInterceptor{oiconsume}
	consumerConfig.Consumer.Offsets.AutoCommit.Enable = true
	consumerConfig.Consumer.Offsets.AutoCommit.Interval = 1 * time.Second

	consumer, err := sarama.NewConsumer(brokers, consumerConfig)
	if err != nil {
		require.Fail(t, "Failed to create kafka consumer", err)
	}

	defer consumer.Close()

	partConsumer, err := consumer.ConsumePartition("test-topic", 0, sarama.OffsetNewest)
	if err != nil {
		require.Fail(t, "Failed to start consuming partition", err)
	}
	defer partConsumer.Close()

	wg := sync.WaitGroup{}

	go func() {
		wg.Add(1)

		msg, ok := <-partConsumer.Messages() // ожидаем получения сообщения
		if !ok {
			assert.Fail(t, "Failed to consume message")
		}

		someHeader := false

		for _, h := range msg.Headers {
			switch string(h.Key) {
			case someheader:
				someHeader = true
			}
		}

		assert.True(t, someHeader) // Checking the transfer of headers from producer to consumer

		time.Sleep(duration) // waiting for OpenTelemetry provider send tracing on consume message

		found := 0
		for {
			line, err := buf.ReadString('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				assert.Fail(t, "Failed to read line", err)
				break
			}

			if strings.Contains(line, traceId) { // нашли root traceid в трассировке.
				found++
			}
		}
		assert.Equal(t, 4, found) // должно быть 4 вхождения от трассировки producer и consumer

		wg.Done()
	}()

	// kafka producer
	kafkaConfig := sarama.NewConfig()
	oi := NewOTelInterceptor("test service")
	kafkaConfig.Producer.Interceptors = []sarama.ProducerInterceptor{oi}
	kafkaConfig.Producer.RequiredAcks = sarama.WaitForAll // WaitForAll - Ждем подтверждения всех синхронизированный реплик
	kafkaConfig.Producer.Retry.Max = 3                    // максимальное количество попыток передачи сообщения
	kafkaConfig.Producer.Return.Successes = true
	kafkaConfig.ChannelBufferSize = 512

	kafka, err := sarama.NewSyncProducer(brokers, kafkaConfig)
	defer kafka.Close()
	if err != nil {
		assert.Fail(t, "Failed to create kafka producer", err)
	}

	// create message
	msg := &sarama.ProducerMessage{
		Topic: "test-topic",
		Key:   sarama.StringEncoder("test key"),
		Value: sarama.StringEncoder("test value"),
	}

	msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(someheader), Value: []byte("true")})

	msg = SetRootSpanContext(ctx, msg)
	_, _, err = kafka.SendMessage(msg)

	wg.Wait()

	buf.Reset()
}
