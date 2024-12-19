package main

import (
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/mrbuk/gcs-compressor/core"
)

var (
	compressionLevel      int
	sourceBucketName      string
	sourceObjectName      string
	destinationBucketName string
	destinationObjectName string
	subscriptionName      string
	topicName             string
	projectId             string

	subscription *pubsub.Subscription
	topic        *pubsub.Topic

	mainCtx    context.Context
	mainCancel context.CancelFunc
)

const WORKFLOW_TIMEOUT = 60 * time.Minute

func init() {
	flag.IntVar(&compressionLevel, "compressionLevel", gzip.DefaultCompression, "NoCompression = 0, BestSpeed = 1, BestCompression = 9, DefaultCompression = -1, HuffmanOnly = -2")
	flag.StringVar(&sourceBucketName, "sourceBucket", "", "name of bucket to read from: e.g. gcs-source-bucket [required]")
	flag.StringVar(&destinationBucketName, "destinationBucket", "", "name of bucket to write to: e.g. gcs-destination bucket [required]")

	flag.StringVar(&sourceObjectName, "sourceObjectName", "", "name of uncompressed source object [cli-driven]")
	flag.StringVar(&destinationObjectName, "destinationObjectName", "", "name of compressed destination object [cli-driven]")

	flag.StringVar(&subscriptionName, "subscription", "", "name of the PubSub subscription to listen for storage notifications [event-driven]")
	flag.StringVar(&topicName, "topic", "", "name of the PubSub topic used to republish messages in case of a shutdown mid-processing [event-driven]")
	flag.StringVar(&projectId, "projectId", pubsub.DetectProjectID, "Google Cloud project id used for the PubSub client")
	flag.Parse()
}

func validateFlags() {
	// check for required values
	if sourceBucketName == "" || destinationBucketName == "" {
		fmt.Fprintf(flag.CommandLine.Output(), "error:	-sourceBucket and -destinationBucket are required\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// ensure that only one sourceObjectName or subscription is set
	if (sourceObjectName == "" && subscriptionName == "") || (sourceObjectName != "" && subscriptionName != "") {
		fmt.Fprintf(flag.CommandLine.Output(), "error:	provide either -sourceObjectName for cli xor -subscription\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if destinationObjectName == "" {
		destinationObjectName = sourceObjectName
	}

	if sourceBucketName == destinationBucketName && sourceObjectName == destinationObjectName {
		fmt.Fprintf(flag.CommandLine.Output(),
			"error:	when using the same -sourceBucket and -destinationBucket, -subscription cannot be used.\n"+
				"	when using the same -sourceBucket and -destinationBucket, -destinationObjectName must be different from -sourceObjectName\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if subscriptionName != "" && topicName == "" {
		fmt.Fprintf(flag.CommandLine.Output(), "error:	when usign -subscription -topic needs to be provided.\n\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
}

func main() {
	validateFlags()

	// use two different context to allow to cancel workers and giving them
	// time to cleanup / republish messages that have not been fully processed
	mainCtx, mainCancel = context.WithCancel(context.Background())
	workerCtx, workerCancel := context.WithCancel(mainCtx)

	// single file should be compressed
	if sourceObjectName != "" {
		wf, err := core.NewWorkflow(mainCtx, compressionLevel, sourceBucketName, sourceObjectName, destinationBucketName, destinationObjectName)
		if err != nil {
			log.Fatalf("error with storage client: %v", err)
		}
		defer wf.Close()

		err = wf.Compress(mainCtx)
		if err != nil {
			log.Fatalf("error compressing object: %v", err)
		}

		err = wf.Delete(mainCtx)
		if err != nil {
			log.Fatalf("error deleting source object: %v", err)
		}

		return
	}

	// event driven
	pubSubClient, err := pubsub.NewClient(workerCtx, projectId)
	if err != nil {
		log.Fatal(err)
	}

	noOfConcurrentJob := runtime.NumCPU() - 1
	if noOfConcurrentJob <= 0 {
		noOfConcurrentJob = 1
	}

	// create a worker pool to paralellize compression
	jobs := make(chan core.WorkflowContext, noOfConcurrentJob)
	for w := 1; w <= noOfConcurrentJob; w++ {
		go worker(workerCtx, w, jobs)
	}

	log.Printf("topic used for republishing '%s'", topicName)
	topic = pubSubClient.Topic(topicName)

	log.Printf("subscribing to '%s'\n", subscriptionName)
	subscription = pubSubClient.Subscription(subscriptionName)

	c := shutdownSignal(mainCancel, workerCancel)
	defer func() {
		signal.Stop(c)
		close(jobs)
		topic.Stop()
		pubSubClient.Close()
	}()

	log.Printf("waiting for messages on '%s'\n", subscriptionName)
	err = subscription.Receive(workerCtx, func(_ context.Context, msg *pubsub.Message) {
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
		jobs <- core.WorkflowContext{
			ObjectName:                objectId,
			OriginalMessageAttributes: msg.Attributes,
			OriginalMessageData:       msg.Data,
		}
	})

	if err != nil {
		log.Fatal("sub.Receive: %w", err)
	}

	<-mainCtx.Done()
}

func worker(ctx context.Context, id int, jobs <-chan core.WorkflowContext) {
	for cdata := range jobs {
		workerName := fmt.Sprintf("[worker-%d]", id)
		objectName := cdata.ObjectName

		newContextData := core.WorkflowContext{
			WorkerName:                workerName,
			ObjectName:                cdata.ObjectName,
			OriginalMessageAttributes: cdata.OriginalMessageAttributes,
			OriginalMessageData:       cdata.OriginalMessageData,
		}

		log.Printf("%s - '%s' compressing from bucket / '%s' -> bucket '%s' / '%s'", workerName, objectName, sourceBucketName, destinationBucketName, objectName)
		func() {

			lctx, lcancel := context.WithTimeout(context.WithValue(ctx, core.ContextData, newContextData), WORKFLOW_TIMEOUT)
			defer lcancel()
			wf, err := core.NewWorkflow(lctx, compressionLevel, sourceBucketName, objectName, destinationBucketName, objectName)
			if err != nil {
				handleWorkerError(lctx, "failed with error with storage client", err)
				return
			}
			defer wf.Close()

			err = wf.Compress(lctx)
			if err != nil {
				handleWorkerError(lctx, "failed with error compressing object", err)
				return
			}

			err = wf.Delete(lctx)
			if err != nil {
				handleWorkerError(lctx, "failed with error deleting source object", err)
				return
			}
			log.Printf("%s - finished job for %s\n", workerName, objectName)
		}()
	}
}

func handleWorkerError(ctx context.Context, errMsg string, cause error) {
	cdata, err := core.GetContextData(ctx)
	if err != nil {
		log.Printf("cannot republish message due to issue with context: %v", err)
	}

	workerName := cdata.WorkerName
	objectName := cdata.ObjectName

	log.Printf("%s - '%s' %s: %v", workerName, objectName, errMsg, cause)

	if cause == context.Canceled || errors.Unwrap(cause) == context.Canceled {
		log.Printf("%s - '%s' context canceled. re-publishing message for reprocessing", workerName, objectName)
		nCtx, _ := context.WithTimeout(mainCtx, 5*time.Second)
		r := topic.Publish(nCtx, &pubsub.Message{
			Attributes: cdata.OriginalMessageAttributes,
			Data:       cdata.OriginalMessageData,
		})
		msgId, err := r.Get(nCtx)
		if err != nil {
			log.Printf("'%s' - error republishing message on topic: %v", objectName, err)
			return
		}
		log.Printf("'%s' - republished message with id '%s'", objectName, msgId)
	}
}

func shutdownSignal(mainCancel, workerCancel context.CancelFunc) chan<- os.Signal {
	// catch SIGINT and properly cancel and cleanup
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-c
		signal.Stop(c)
		log.Printf("received signal %v", sig)

		// cancel the workers, wait 7s - the docker default timeout before forefully killing is 10s -
		// to allow for cleanup / republishing of messages
		log.Printf("canceling all workers and waiting 7s before stopping - issue another signal to kill immediatlely")
		workerCancel()
		time.Sleep(7 * time.Second)
		mainCancel()
	}()

	return c
}
