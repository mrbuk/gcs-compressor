package core

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"cloud.google.com/go/storage"
)

type WorkflowContextKey int

type WorkflowContext struct {
	WorkerName                string
	ObjectName                string
	OriginalMessageAttributes map[string]string
	OriginalMessageData       []byte
}

var ContextData WorkflowContextKey

type Workflow struct {
	client           *storage.Client
	srcObject        *storage.ObjectHandle
	dstObject        *storage.ObjectHandle
	compressionLevel int
}

func NewWorkflow(ctx context.Context, compressionLevel int, sourceBucketName, sourceObjectName, destinationBucketName, destinationObjectName string) (*Workflow, error) {
	c := &Workflow{}

	c.compressionLevel = compressionLevel

	var err error
	if c.client, err = storage.NewClient(ctx); err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %v", err)
	}

	srcBucket := c.client.Bucket(sourceBucketName)
	c.srcObject = srcBucket.Object(sourceObjectName)

	dstBucket := c.client.Bucket(destinationBucketName)
	c.dstObject = dstBucket.Object(destinationObjectName)

	return c, nil
}

func (c *Workflow) Close() {
	c.client.Close()
}

func GetContextData(ctx context.Context) (WorkflowContext, error) {
	data, ok := ctx.Value(ContextData).(WorkflowContext)
	if !ok {
		return WorkflowContext{}, errors.New("unexpected value of context")
	}
	return data, nil
}

func GetWorkerName(ctx context.Context) string {
	defaultName := "[default]"

	data, ok := ctx.Value(ContextData).(WorkflowContext)
	if !ok || data.WorkerName == "" {
		return defaultName
	}

	return data.WorkerName
}

// Compress reads a source file in GCS and writes it GZIP compressed to GCS
func (c *Workflow) Compress(ctx context.Context) error {
	workerName := GetWorkerName(ctx)

	// Open the source object for reading
	srcReader, err := c.srcObject.NewReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to open source object: %w", err)
	}
	defer srcReader.Close()

	srcObjectAttrs, err := c.srcObject.Attrs(ctx)
	if err != nil {
		return fmt.Errorf("cannot determine source object size: %w", err)
	}

	if c.dstObjectExists(ctx) {
		return fmt.Errorf("destination object exists already")
	}

	bytesProcessed, err := (func() (int64, error) {
		dstWriter := c.dstObject.NewWriter(ctx)
		defer dstWriter.Close()

		// Set appropriate content type and encoding for the destination object
		dstWriter.ContentType = srcObjectAttrs.ContentType
		dstWriter.ContentEncoding = "gzip"

		// Create a GZIP writer wrapping the GCS writer
		gzipWriter, _ := gzip.NewWriterLevel(dstWriter, c.compressionLevel)
		defer gzipWriter.Close()

		// Stream from the source object to the GZIP writer (and then to GCS)
		log.Printf("%s - '%s' reading file from bucket '%s' and to writing compressed to '%s/%s'", workerName, c.srcObject.ObjectName(), c.srcObject.BucketName(), c.dstObject.BucketName(), c.dstObject.ObjectName())
		n, err := io.Copy(gzipWriter, srcReader)
		if err != nil {
			return -1, fmt.Errorf("failed to compress and upload object: %w", err)
		}

		return n, nil
	})()
	if err != nil {
		return err
	}

	dstObjectAttrs, err := c.dstObject.Attrs(ctx)
	if err != nil {
		return fmt.Errorf("failed to read destination object metadata: %w", err)
	}

	var compressionRatio float64
	if dstObjectAttrs.Size > 0 {
		compressionRatio = float64(srcObjectAttrs.Size) / float64(dstObjectAttrs.Size)
	}
	log.Printf("%s - '%s' read %d bytes from file of size %d", workerName, c.srcObject.ObjectName(), bytesProcessed, srcObjectAttrs.Size)
	log.Printf("%s - '%s' compressed %d bytes to %d bytes in %s/%s. Compression ratio %.2f", workerName, c.srcObject.ObjectName(), bytesProcessed, dstObjectAttrs.Size, c.dstObject.BucketName(), c.dstObject.ObjectName(), compressionRatio)

	return nil
}

func (c *Workflow) dstObjectExists(ctx context.Context) bool {
	_, err := c.dstObject.Attrs(ctx)
	return err == nil
}

func (c *Workflow) Delete(ctx context.Context) error {
	workerName := GetWorkerName(ctx)

	log.Printf("%s - '%s' initiating deletion of source file in bucket %s", workerName, c.srcObject.ObjectName(), c.srcObject.BucketName())
	if err := c.srcObject.Delete(ctx); err != nil {
		return fmt.Errorf("error deleting source file: %w", err)
	}
	log.Printf("%s - '%s' source file in bucket %s successfully deleted", workerName, c.srcObject.ObjectName(), c.srcObject.BucketName())

	return nil
}
