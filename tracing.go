package otelsarama

import (
	"context"
	"github.com/IBM/sarama"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"reflect"
	"strconv"
)

// These based on OpenTelemetry API & SDKs for go
// https://opentelemetry.io/docs/languages/go/

// Semantic Conventions for Kafka 21.10.2024
// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/messaging/kafka.md @f1c64ca

// Semantic Conventions for Messaging Spans 21.10.2024
// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/messaging/messaging-spans.md @f1c64ca

// General Attributes 15.10.2024
// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/attributes.md#general-remote-service-attributes @d5d2b9d

type OTelInterceptor struct {
	tracer     trace.Tracer
	fixedAttrs []attribute.KeyValue
}

const (
	otelLibraryName = "github.com/arslanovdi/otelsarama"
	otelLibraryVer  = "v0.1.1"

	TraceHeaderName   = "trace_id"
	SpanHeaderName    = "span_id"
	SampledHeaderName = "sampled"
	RetryHeaderName   = "retry"
)

// shouldIgnoreMsg
// check for trace attributes to prevent sending trace messages during retries.
func shouldIgnoreMsg(msg *sarama.ProducerMessage) bool {
	var retryFound bool
	for _, h := range msg.Headers {
		if string(h.Key) == RetryHeaderName {
			retryFound = true
			continue
		}
	}
	return retryFound
}

// Context
// Returns a context with a trace to create a child trace.
// On any error, context.Background() is returned.
func Context[T sarama.ProducerMessage | sarama.ConsumerMessage](msg *T) context.Context {
	ctx := context.Background()
	if msg == nil {
		return ctx
	}
	roottraceid := ""
	rootspanid := ""
	issampled := trace.TraceFlags(0)

	switch reflect.TypeOf(msg).String() {
	case "*sarama.ProducerMessage":
		m, ok := any(msg).(*sarama.ProducerMessage)
		if !ok {
			return ctx
		}
		for _, h := range m.Headers {
			switch string(h.Key) {
			case TraceHeaderName:
				roottraceid = string(h.Value)
			case SpanHeaderName:
				rootspanid = string(h.Value)
			case SampledHeaderName:
				issampled = trace.FlagsSampled
			}
		}

	case "*sarama.ConsumerMessage":
		m, ok := any(msg).(*sarama.ConsumerMessage)
		if !ok {
			return ctx
		}
		for _, h := range m.Headers {
			switch string(h.Key) {
			case TraceHeaderName:
				roottraceid = string(h.Value)
			case SpanHeaderName:
				rootspanid = string(h.Value)
			case SampledHeaderName:
				issampled = trace.FlagsSampled
			}
		}

	default:
		return ctx
	}

	if len(roottraceid) == 0 || len(rootspanid) == 0 {
		return ctx
	}

	traceid, err := trace.TraceIDFromHex(roottraceid)
	if err != nil {
		return ctx
	}
	spanid, err := trace.SpanIDFromHex(rootspanid)
	if err != nil {
		return ctx
	}

	ctx = trace.ContextWithRemoteSpanContext(
		context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceid,
			SpanID:     spanid,
			TraceFlags: issampled,
		}),
	)

	return ctx
}

// OnSend
// tracing producer message.
func (oi *OTelInterceptor) OnSend(msg *sarama.ProducerMessage) {
	if shouldIgnoreMsg(msg) { // exclude retry messages
		return
	}

	attWithTopic := append(
		oi.fixedAttrs,
		attribute.String("messaging.destination.name", msg.Topic),
		attribute.String("messaging.destination.partition.id", strconv.FormatInt(int64(msg.Partition), 10)),
		attribute.String("messaging.operation.name", "send"),
		attribute.String("messaging.operation.type", "send"),
	)

	key, err := msg.Key.Encode()
	if err == nil {
		if len(key) > 0 { //  If the key is null, the attribute MUST NOT be set
			attWithTopic = append(attWithTopic, attribute.String("messaging.kafka.message.key", string(key)))
		}
	} else {
		slog.Debug("key encoding error", slog.String("error", err.Error()))
	}

	_, span := oi.tracer.Start(
		Context(msg),
		msg.Topic,
		trace.WithAttributes(attWithTopic...))

	defer span.End()
	spanContext := span.SpanContext()

	span.SetAttributes(attribute.String("messaging.message.id", spanContext.SpanID().String()))

	setSpanAttributes(spanContext, msg)
	msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(RetryHeaderName), Value: []byte("true")})
}

// OnConsume
// tracing consumer message.
func (oi *OTelInterceptor) OnConsume(msg *sarama.ConsumerMessage) {

	attWithTopic := append(
		oi.fixedAttrs,
		attribute.String("messaging.destination.name", msg.Topic),
		///attribute.String("messaging.consumer.group.name", "my-group"),
		attribute.String("messaging.destination.partition.id", strconv.FormatInt(int64(msg.Partition), 10)),
		attribute.String("messaging.operation.name", "poll"),
		attribute.String("messaging.operation.type", "receive"),
		attribute.String("messaging.kafka.message.offset", strconv.FormatInt(msg.Offset, 10)),
	)

	if len(msg.Key) > 0 { //  If the key is null, the attribute MUST NOT be set
		attWithTopic = append(attWithTopic, attribute.String("messaging.kafka.message.key", string(msg.Key)))
	}

	_, span := oi.tracer.Start(
		Context(msg),
		msg.Topic,
		trace.WithAttributes(attWithTopic...))
	defer span.End()
	spanContext := span.SpanContext()

	span.SetAttributes(attribute.String("messaging.message.id", spanContext.SpanID().String()))

	setSpanAttributes(spanContext, msg)
}

// NewOTelInterceptor - implements the sarama producer/consumer interceptor interface for OpenTelemetry tracing.
// Global TraceProvider must be registered.
// instance - unique identifier of the service instance.
func NewOTelInterceptor(instance string) *OTelInterceptor {

	oi := OTelInterceptor{}
	oi.tracer = otel.GetTracerProvider().Tracer(otelLibraryName, trace.WithInstrumentationVersion(otelLibraryVer))

	oi.fixedAttrs = []attribute.KeyValue{
		attribute.String("messaging.client.id", instance),
		attribute.String("messaging.system", "kafka"),
	}

	return &oi
}

// SetRootSpanContext
// Integrates into producer message root span from context if it exists.
func SetRootSpanContext(ctx context.Context, msg *sarama.ProducerMessage) *sarama.ProducerMessage {

	span := trace.SpanFromContext(ctx)

	if span.SpanContext().HasTraceID() == false {
		return msg
	}

	headers := []sarama.RecordHeader{
		{Key: []byte(TraceHeaderName), Value: []byte(span.SpanContext().TraceID().String())},
		{Key: []byte(SpanHeaderName), Value: []byte(span.SpanContext().SpanID().String())},
	}
	if span.SpanContext().IsSampled() {
		headers = append(headers, sarama.RecordHeader{Key: []byte(SampledHeaderName), Value: []byte("true")})
	}
	msg.Headers = append(msg.Headers, headers...)

	return msg
}

// setSpanAttributes
// setting partial tracing headers to create child span.
func setSpanAttributes[T sarama.ProducerMessage | sarama.ConsumerMessage](spanContext trace.SpanContext, msg *T) {
	if msg == nil {
		return
	}

	switch reflect.TypeOf(msg).String() {
	case "*sarama.ProducerMessage":
		m, ok := any(msg).(*sarama.ProducerMessage)
		if !ok {
			return
		}
		// remove existing partial tracing headers if exists
		noTraceHeaders := m.Headers[:0]
		for _, h := range m.Headers {
			key := string(h.Key)
			if key != TraceHeaderName && key != SpanHeaderName && key != SampledHeaderName && key != RetryHeaderName {
				noTraceHeaders = append(noTraceHeaders, h)
			}
		}
		traceHeaders := []sarama.RecordHeader{
			{Key: []byte(TraceHeaderName), Value: []byte(spanContext.TraceID().String())},
			{Key: []byte(SpanHeaderName), Value: []byte(spanContext.SpanID().String())},
		}
		if spanContext.IsSampled() {
			traceHeaders = append(traceHeaders, sarama.RecordHeader{Key: []byte(SampledHeaderName), Value: []byte("true")})
		}
		m.Headers = append(noTraceHeaders, traceHeaders...)
		return

	case "*sarama.ConsumerMessage":
		m, ok := any(msg).(*sarama.ConsumerMessage)
		if !ok {
			return
		}
		// remove existing partial tracing headers if exists
		noTraceHeaders := m.Headers[:0]
		for _, h := range m.Headers {
			key := string(h.Key)
			if key != TraceHeaderName && key != SpanHeaderName && key != SampledHeaderName && key != RetryHeaderName {
				noTraceHeaders = append(noTraceHeaders, h)
			}
		}
		traceHeaders := []*sarama.RecordHeader{
			{Key: []byte(TraceHeaderName), Value: []byte(spanContext.TraceID().String())},
			{Key: []byte(SpanHeaderName), Value: []byte(spanContext.SpanID().String())},
		}
		if spanContext.IsSampled() {
			traceHeaders = append(traceHeaders, &sarama.RecordHeader{Key: []byte(SampledHeaderName), Value: []byte("true")})
		}
		m.Headers = append(noTraceHeaders, traceHeaders...)
		return

	default:
		return
	}
}
