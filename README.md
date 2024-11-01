# OpenTelemetry instrumentation for [sarama](https://github.com/IBM/sarama)

Библиотека реализует трассировку сообщений в/из Kafka с сохранением контекста.

### Install
Manual install:
```bash
go get -u github.com/arslanovdi/otelsarama 
```

Golang import:

```go
import "github.com/arslanovdi/otelsarama"
```

### Example

[пример использования библиотеки](https://github.com/arslanovdi/otelsarama/tree/main/example)

### Usage

В приложении должен быть зарегистрирован глобальный провайдер трассировки.

Настройка конфигурации sarama для producer и consumer:
```go
cfg := sarama.NewConfig()
cfg.Producer.Interceptors = []sarama.ProducerInterceptor{
	otelsarama.NewOTelInterceptor("service instance id"),
	}
cfg.Consumer.Interceptors = []sarama.ConsumerInterceptor{
    otelsarama.NewOTelInterceptor("service instance id"),
}
```
Для проброса родительского spanID из контекста в producer необходимо вызвать функцию SetRootSpanContext перед отправкой сообщения в кафку.

```go
msg = otelsarama.SetRootSpanContext(ctx, msg)
```

Для дальнейшего проброса контекста трассировки после получения сообщения из кафки нужно вызвать функцию Context. Новый Span создавать основываясь на этом контексте.

```go
ctx := otelsarama.Context(msg)
```

### Problems
Полностью соответствовать стандарту не выйдет.

- нет возможности перехватывать ошибки при приеме/отправке сообщений.
- нет возможности перехватывать пакеты сообщений, только одиночные сообщения.
- в sarama только 1 интерцептор OnConsume, т.е. обрабатываем только span poll. span process, span commit не обрабатываются 

sarama не поддерживает go context, для интеграции с родительским контекстом трассировки вынуждены пробрасывать root traceid, spanid внутри сообщения.

### Documentation used
[OpenTelemetry API & SDKs for go](https://opentelemetry.io/docs/languages/go/)

[Semantic Conventions for Kafka](https://github.com/open-telemetry/semantic-conventions/blob/main/docs/messaging/kafka.md)

