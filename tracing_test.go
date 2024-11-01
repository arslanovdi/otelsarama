package otelsarama

import (
	"bytes"
	"context"
	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"os"
	"testing"
	"time"
)

var tracer = otel.GetTracerProvider().Tracer(
	"test service",
	trace.WithSchemaURL(semconv.SchemaURL),
)

var buf bytes.Buffer // буфер для вывода трэйсинга

func TestMain(m *testing.M) {

	exporter, err := stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
		stdouttrace.WithWriter(&buf),
	)

	if err != nil {
		os.Exit(1)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(
			resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String("test service"),
			),
		),
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(500*time.Millisecond)),
		sdktrace.WithSampler(
			sdktrace.AlwaysSample(),
		),
	)

	otel.SetTracerProvider(provider)

	defer provider.Shutdown(context.Background())

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	code := m.Run()
	os.Exit(code)
}

func TestContext(t *testing.T) {
	t.Run("msg == nil", func(t *testing.T) {
		t.Parallel()
		msg := &sarama.ProducerMessage{}
		msg = nil
		ctx := Context(msg)
		assert.Equal(t, ctx, context.Background())
	})
	t.Run("msg == sarama.ProducerMessage without span Headers", func(t *testing.T) {
		t.Parallel()
		msg := &sarama.ProducerMessage{}
		ctx := Context(msg)
		assert.Equal(t, ctx, context.Background())
	})
	t.Run("msg == sarama.ProducerMessage with span Headers", func(t *testing.T) {
		t.Parallel()

		ctx, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		traceId := span.SpanContext().TraceID().String()
		spanId := span.SpanContext().SpanID().String()

		msg := &sarama.ProducerMessage{}
		msg = SetRootSpanContext(ctx, msg) // привязали span к msg

		ctx = Context(msg) // вытаскиваем span из msg
		assert.Equal(t, traceId, trace.SpanFromContext(ctx).SpanContext().TraceID().String())
		assert.Equal(t, spanId, trace.SpanFromContext(ctx).SpanContext().SpanID().String())
	})
	t.Run("msg == sarama.ConsumerMessage without span Headers", func(t *testing.T) {
		t.Parallel()
		msg := &sarama.ConsumerMessage{}
		ctx := Context(msg)
		assert.Equal(t, ctx, context.Background())
	})
	t.Run("msg == sarama.ConsumerMessage with span Headers", func(t *testing.T) {
		t.Parallel()
		ctx, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		traceId := span.SpanContext().TraceID().String()
		spanId := span.SpanContext().SpanID().String()

		msg := &sarama.ConsumerMessage{}
		setSpanAttributes(span.SpanContext(), msg) // привязали span к msg

		ctx = Context(msg) // вытаскивлем span из msg
		assert.Equal(t, traceId, trace.SpanFromContext(ctx).SpanContext().TraceID().String())
		assert.Equal(t, spanId, trace.SpanFromContext(ctx).SpanContext().SpanID().String())
	})
	buf.Reset()
}

func TestSetRootSpanContext(t *testing.T) {
	t.Run("span ctx to msg", func(t *testing.T) {
		ctx, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		traceId := span.SpanContext().TraceID().String()
		spanId := span.SpanContext().SpanID().String()

		msg := &sarama.ProducerMessage{}

		SetRootSpanContext(ctx, msg) // привязали span к msg

		sampled := false
		traceid2 := ""
		spanid2 := ""
		for _, h := range msg.Headers {
			switch string(h.Key) {
			case TraceHeaderName:
				traceid2 = string(h.Value)
			case SpanHeaderName:
				spanid2 = string(h.Value)
			case SampledHeaderName:
				sampled = true
			}
		}
		assert.Equal(t, traceId, traceid2)
		assert.Equal(t, spanId, spanid2)
		assert.Equal(t, sampled, span.SpanContext().IsSampled())
	})
	buf.Reset()
}

func Test_setSpanAttributes(t *testing.T) {
	t.Parallel()
	t.Run("msg == nil", func(t *testing.T) { // Тут нет asserts, достаточно того что не вылетел nil pointer dereference
		msg := &sarama.ProducerMessage{}
		msg = nil
		setSpanAttributes(trace.SpanContext{}, msg)
	})
	t.Run("producermessage", func(t *testing.T) {
		ctx, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		traceId := span.SpanContext().TraceID().String()
		spanId := span.SpanContext().SpanID().String()

		msg := &sarama.ProducerMessage{}
		SetRootSpanContext(ctx, msg)
		setSpanAttributes(span.SpanContext(), msg)

		sampled := false
		traceid2 := ""
		spanid2 := ""
		for _, h := range msg.Headers {
			switch string(h.Key) {
			case TraceHeaderName:
				traceid2 = string(h.Value)
			case SpanHeaderName:
				spanid2 = string(h.Value)
			case SampledHeaderName:
				sampled = true
			}
		}
		assert.Equal(t, traceId, traceid2)
		assert.Equal(t, spanId, spanid2)
		assert.Equal(t, sampled, span.SpanContext().IsSampled())

	})
	t.Run("consumermessage", func(t *testing.T) {
		_, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		traceId := span.SpanContext().TraceID().String()
		spanId := span.SpanContext().SpanID().String()

		msg := &sarama.ConsumerMessage{}

		setSpanAttributes(span.SpanContext(), msg)

		sampled := false
		traceid2 := ""
		spanid2 := ""
		for _, h := range msg.Headers {
			switch string(h.Key) {
			case TraceHeaderName:
				traceid2 = string(h.Value)
			case SpanHeaderName:
				spanid2 = string(h.Value)
			case SampledHeaderName:
				sampled = true
			}
		}
		assert.Equal(t, traceId, traceid2)
		assert.Equal(t, spanId, spanid2)
		assert.Equal(t, sampled, span.SpanContext().IsSampled())

	})
	buf.Reset()
}

func Test_shouldIgnoreMsg(t *testing.T) {
	t.Parallel()
	t.Run("msg without span", func(t *testing.T) {
		msg := &sarama.ProducerMessage{}
		retry := shouldIgnoreMsg(msg)
		assert.Equal(t, false, retry)
	})
	t.Run("msg with span", func(t *testing.T) {
		_, span := tracer.Start(context.Background(), "span name in test") // сгенерировали span
		defer span.End()
		msg := &sarama.ProducerMessage{}
		setSpanAttributes(span.SpanContext(), msg)
		msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(RetryHeaderName), Value: []byte("true")})

		retry := shouldIgnoreMsg(msg)
		assert.Equal(t, true, retry)
	})
	buf.Reset()
}
