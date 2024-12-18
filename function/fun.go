package function

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	workflow "github.com/mrbuk/gcs-compressor/core"
)

// CloudStorageEvent represents the structure of a Cloud Storage event.
type CloudStorageEvent struct {
	Bucket      string `json:"bucket"`
	Name        string `json:"name"`
	TimeCreated string `json:"timeCreated"`
}

type HttpError struct {
	Message string
	Code    int
}

func init() {
	functions.HTTP("compress", Compress)
}

// Compress is an HTTP Cloud Function with a request parameter.
func Compress(w http.ResponseWriter, r *http.Request) {
	compressionLevel := gzip.BestSpeed
	destinationBucketName := os.Getenv("DESTINATION_BUCKET")

	if destinationBucketName == "" {
		handleError(w, &HttpError{
			Message: "provide DESTINATION_BUCKET env variable",
			Code:    http.StatusInternalServerError,
		})
		return
	}

	event, httpErr := decodeData(r)
	if httpErr != nil {
		handleError(w, httpErr)
		return
	}

	// ingore files containing 'dax-tmp'
	if event.Name == "" || strings.Contains(event.Name, "dax-tmp") {
		log.Printf("ignoring event for temp object: '%s'\n", event.Name)
		return
	}

	// compress all other files
	ctx := context.Background()
	wf, err := workflow.NewWorkflow(ctx, compressionLevel, event.Bucket, event.Name, destinationBucketName, event.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer wf.Close()

	err = wf.Compress()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = wf.Delete()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func decodeData(r *http.Request) (CloudStorageEvent, *HttpError) {
	var event CloudStorageEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		return CloudStorageEvent{}, &HttpError{fmt.Sprintf("error unmarshalling JSON: %v", err), http.StatusBadRequest}
	}
	return event, nil
}

func handleError(w http.ResponseWriter, err *HttpError) {
	log.Printf("error: %s", err.Message)
	http.Error(w, err.Message, err.Code)
}
