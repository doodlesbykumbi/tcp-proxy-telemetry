package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/metric/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/unit"
)

func initMeter() {
	exporter, err := prometheus.InstallNewPipeline(prometheus.Config{})
	if err != nil {
		log.Panicf("failed to initialize prometheus exporter %v", err)
	}
	http.HandleFunc("/metrics", exporter.ServeHTTP)
	go func() {
		err := http.ListenAndServe(":2222", nil)
		if err != nil {
			panic(err)
		}
	}()

	fmt.Println("Prometheus server running on :2222")
}

const closedConnectionErrString = "use of closed network connection"

func duplexStream(
	source io.ReadWriter,
	destination io.ReadWriter,
) (sourceErrorChan <-chan error, destinationErrorChan <-chan error) {
	_sourceErrorChan := make(chan error)
	_destinationErrorChan := make(chan error)

	go func() {
		_sourceErrorChan <- stream(source, destination)
	}()
	go func() {
		_destinationErrorChan <- stream(destination, source)
	}()

	sourceErrorChan = _sourceErrorChan
	destinationErrorChan = _destinationErrorChan
	return
}

func stream(source io.Reader, destination io.Writer) error {
	_, err := io.Copy(destination, source)
	return err
}

type ReadWriteNotifier struct {
	readWriter io.ReadWriter
	onWrite    func(int, time.Duration)
	onRead     func(int, time.Duration)
}

// Write implements the io.ReadWriter interface.
func (rwn *ReadWriteNotifier) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := rwn.readWriter.Write(p)
	if err == nil && rwn.onWrite != nil {
		rwn.onWrite(n, time.Now().Sub(start))
	}

	return n, err
}

// Read implements the io.ReadWriter interface.
func (rwn *ReadWriteNotifier) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := rwn.readWriter.Read(p)
	if err == nil && rwn.onRead != nil {
		rwn.onRead(n, time.Now().Sub(start))
	}

	return n, err
}

func main() {
	arguments := os.Args
	if len(arguments) < 3 {
		fmt.Println("Please provide listener port number and upstream host:port")
		return
	}

	initMeter()
	meter := global.Meter("my_application")
	requestBytes, err := meter.NewInt64Counter("request.bytes", metric.WithUnit(unit.Bytes))
	if err != nil {
		fmt.Println(err)
		return
	}
	requestLatency, err := meter.NewInt64ValueRecorder("request.latency")
	if err != nil {
		fmt.Println(err)
		return
	}

	PORT := ":" + arguments[1]
	CONNECT := arguments[2]
	l, err := net.Listen("tcp", PORT)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer l.Close()

	ctx := context.Background()
	for {
		clientConn, err := l.Accept()
		if err != nil {
			fmt.Println("FAIL ACCEPT:", err)
			break
		}
		fmt.Println("ACCEPTED")

		targetConn, err := net.Dial("tcp", CONNECT)
		if err != nil {
			fmt.Println("CONNECT:", err)
			break
		}


		_, _ = duplexStream(
			&ReadWriteNotifier{
				readWriter:  clientConn,
				onWrite: func(n int, duration time.Duration) {
					requestBytes.Add(ctx, int64(n))
					requestLatency.Record(ctx, duration.Milliseconds())
					log.Printf("%d bytes written in %s", n, duration)
				},
				onRead: func(n int, duration time.Duration) {
					//requestBytes.Add(ctx, int64(n))
					//requestLatency.Record(ctx, duration.Milliseconds())
				},
			}, &ReadWriteNotifier{
				readWriter:  targetConn,
				onWrite: func(n int, duration time.Duration) {
					requestBytes.Add(ctx, int64(n))
					requestLatency.Record(ctx, duration.Microseconds())
					log.Printf("%d bytes written in %s", n, duration)
				},
				onRead: func(n int, duration time.Duration) {
					//requestBytes.Add(ctx, int64(n))
					//requestLatency.Record(ctx, duration.Milliseconds())
				},
			})
		fmt.Println("STREAMING")
	}
}
