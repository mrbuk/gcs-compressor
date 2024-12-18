package main

import (
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/mrbuk/gcs-compressor/core"
)

var (
	compressionLevel      int
	sourceBucketName      string
	sourceObjectName      string
	destinationBucketName string
	destinationObjectName string
	subscription          string
	projectId             string
)

func init() {
	flag.IntVar(&compressionLevel, "compressionLevel", gzip.DefaultCompression, "NoCompression = 0, BestSpeed = 1, BestCompression = 9, DefaultCompression = -1, HuffmanOnly = -2")
	flag.StringVar(&sourceBucketName, "sourceBucket", "", "name of bucket to read from: e.g. gcs-source-bucket [required]")
	flag.StringVar(&destinationBucketName, "destinationBucket", "", "name of bucket to write to: e.g. gcs-destination bucket [required]")

	flag.StringVar(&sourceObjectName, "sourceObjectName", "", "name of uncompressed source object [cli-driven]")
	flag.StringVar(&destinationObjectName, "destinationObjectName", "", "name of compressed destination object [cli-driven]")

	flag.StringVar(&subscription, "subscription", "", "fully qualified name of the PubSub Subscription to listen for storage notifications [event-driven]")
	flag.StringVar(&projectId, "projectId", pubsub.DetectProjectID, "Google Cloud project id used for the PubSub client")
	flag.Parse()
}

func main() {
	// check for required values
	if sourceBucketName == "" || destinationBucketName == "" {
		fmt.Fprintf(flag.CommandLine.Output(), "error: -sourceBucketName and -destinationBucketName are required\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// ensure that only one sourceObjectName or subscription is set
	if (sourceObjectName == "" && subscription == "") || (sourceObjectName != "" && subscription != "") {
		fmt.Fprintf(flag.CommandLine.Output(), "error: provide either -sourceObjectName for cli xor -subscription\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if destinationObjectName == "" {
		destinationObjectName = sourceObjectName
	}

	ctx, cancel := context.WithCancel(context.Background())

	// single file should be compressed
	if sourceObjectName != "" {
		wf, err := core.NewWorkflow(ctx, compressionLevel, sourceBucketName, sourceObjectName, destinationBucketName, destinationObjectName)
		if err != nil {
			log.Fatalf("error with storage client: %v", err)
		}
		defer wf.Close()

		err = wf.Compress()
		if err != nil {
			log.Fatalf("error compressing object: %v", err)
		}

		err = wf.Delete()
		if err != nil {
			log.Fatalf("error deleting source object: %v", err)
		}

		return
	}

	// event driven
	client, err := pubsub.NewClient(ctx, projectId)
	if err != nil {
		log.Fatal(err)
	}

	noOfConcurrentJob := runtime.NumCPU() - 1
	if noOfConcurrentJob <= 0 {
		noOfConcurrentJob = 1
	}

	// create a worker pool to paralellize compression
	jobs := make(chan string, noOfConcurrentJob)
	for w := 1; w <= noOfConcurrentJob; w++ {
		go worker(ctx, w, jobs)
	}

	// catch SIGINT and properly cancel and cleanup
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	defer func() {
		signal.Stop(c)
		cancel()
	}()
	go func() {
		select {
		case <-c:
			// close PubSub Connection
			client.Close()
			cancel()
			// close all job channels
			close(jobs)
		case <-ctx.Done():
		}
	}()

	log.Printf("subscribing to '%s'\n", subscription)
	sub := client.Subscription(subscription)
	log.Printf("waiting for messages on '%s'\n", subscription)
	err = sub.Receive(ctx, func(_ context.Context, msg *pubsub.Message) {
		bucketId := msg.Attributes["bucketId"]
		if bucketId != sourceBucketName {
			log.Printf("ignoring event - received for bucket '%s' but expected to get it for bucket '%s'. Potentially storage notification misconfigured.\n", bucketId, sourceBucketName)
			msg.Ack()
			return
		}

		objectId := msg.Attributes["objectId"]
		if objectId == "" {
			log.Printf("ignoring event for empty object: %v\n", msg.Attributes)
			msg.Ack()
			return
		}

		// ingore files containing 'dax-tmp'
		if objectId == "" || strings.Contains(objectId, "dax-tmp") {
			log.Printf("ignoring event for temp object: '%s'\n", objectId)
			msg.Ack()
			return
		}

		// ingore events other than finalize (e.g. delete)
		eventType := msg.Attributes["eventType"]
		if eventType != "OBJECT_FINALIZE" {
			log.Printf("ignoring event of type '%s' for objectId '%s'\n", eventType, objectId)
			msg.Ack()
			return
		}

		// write into event into BQ and ack the message directly
		// the max allowed ack deadline for Pubsub is 600s
		// compressing large files takes than 600s resulting into
		// potential duplicates if not acked directly
		// TODO ensure to write to BQ before we ACK
		msg.Ack()
		jobs <- objectId
	})

	if err != nil {
		log.Fatal("sub.Receive: %w", err)
	}
}

func worker(ctx context.Context, id int, jobs <-chan string) {
	for objectId := range jobs {
		workerName := fmt.Sprintf("[worker-%d]", id)
		lctx := context.WithValue(ctx, core.WorkerName, workerName)

		log.Printf("%s - '%s' compressing from bucket / '%s' -> bucket '%s' / '%s'", workerName, objectId, sourceBucketName, destinationBucketName, objectId)
		wf, err := core.NewWorkflow(lctx, compressionLevel, sourceBucketName, objectId, destinationBucketName, objectId)
		if err != nil {
			log.Printf("%s - '%s' failed with error with storage client: %v", workerName, objectId, err)
			return
		}
		defer wf.Close()

		err = wf.Compress()
		if err != nil {
			log.Printf("%s - '%s' failed with error compressing object: %v", workerName, objectId, err)
			return
		}

		err = wf.Delete()
		if err != nil {
			log.Printf("%s - '%s' failed with error deleting source object: %v", workerName, objectId, err)
			return
		}

		log.Printf("%s - finished job for %s\n", workerName, objectId)
	}
}
