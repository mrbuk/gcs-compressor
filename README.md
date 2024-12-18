# gcs-compressor

`gcs-compressor` compresses objects within GCS via GZIP. Similar to `gzip` it deletes the source file after successful compression.

You can run `gcs-compressor` in two modes:

1. interactive  - compress a specific object (your provide source and destination)
2. event-driven - using a Cloud Storage Notification via PubSub (source is coming via PubSub)

The application is written in Go and can either be run 

as a binary

    ```
    $ ./build.sh

    # mode 1 - interactive - copy a specifc file
    $ ./build/gcs-compressor \ 
        -compressionLevel 1 \   #  0 = NoCompression, 1 = BestSpeed ... 9 = BestCompression, -1 = DefaultCompression
        -sourceBucket gcs-compression-source-1f34 \
        -sourceObjectName "100m.txt" \
        -destinationBucket gcs-compression-destination-1f34 \
        -destinationObjectName "100m-new.txt" # provide another filename in case you want to write to the same bucket

    # mode 2 - event-driven - listen for events on subscription 'object-notifier-compressor'
    $ ./build/gcs-compressor \ 
        -compressionLevel 1 \   #  0 = NoCompression, 1 = BestSpeed ... 9 = BestCompression, -1 = DefaultCompression
        -sourceBucket gcs-compression-source-1f34 \
        -destinationBucket gcs-compression-destination-1f34 \
        -subscription object-notifier-compressor -projectId dev-demo-333610 

    ```

or via Docker

    ```
    # mode 1 - interactive - copy a specifc file
    $ docker run -it mrbuk/gcs-compressor:0.1 \ 
        -compressionLevel 1 \   #  0 = NoCompression, 1 = BestSpeed ... 9 = BestCompression, -1 = DefaultCompression
        -sourceBucket gcs-compression-source-1f34 \
        -sourceObjectName "100m.txt" \
        -destinationBucket gcs-compression-destination-1f34 \
        -destinationObjectName "100m-new.txt" # provide another filename in case you want to write to the same bucket

    # mode 2 - event-driven - listen for events on subscription 'object-notifier-compressor'
    $ docker run mrbuk/gcs-compressor:0.1  \ 
        -compressionLevel 1 \   #  0 = NoCompression, 1 = BestSpeed ... 9 = BestCompression, -1 = DefaultCompression
        -sourceBucket gcs-compression-source-1f34 \
        -destinationBucket gcs-compression-destination-1f34 \
        -subscription object-notifier-compressor -projectId dev-demo-333610 
    ```

**Important:** PubSub Messages are acknowledged right before the compression operation starts. 
This is due to the fact that compressing a single file can take longer than the current existing ACK Deadline. 
For that reason errors have to handled manually by either re-sending a event into PubSub or running it in mode 1 - interactive.

## Permissions

`gcs-compressor` requires following permissions
  - PubSub Subscriber (on the subscription)
  - Storage Object User (on source and destination bucket)

# Runtime environment

It is possible to have `gcs-compressor` be run as a Cloud Function. Due to the fact the a compression operation can take long than 540s or 900s it is not recommended.

Instead the recommendation is to run `gcs-compressor` via a Container directly in Google Compute Engine (GCE) via Google Container OS. GCE allows also for optmizions e.g.

- custom instance types with a few RAM as possible (e.g. `n2-custom-16-8192`)
- disabling HyperThreading (e.g. `n2-custom-16-8192` shows as 8 cores instead of 16)

The follwoing `gcloud` command will deploy an instance using COS in `europe-west4` pulling the container image from Docker Hub:

    ```
    $ gcloud compute instances create-with-container gcs-compressor-main \
        --project=${GCP_PROJECT_ID} \
        --zone=europe-west4-c \
        --machine-type=n2-custom-16-8192 \
        --network-interface=network-tier=PREMIUM,stack-type=IPV4_ONLY,subnet=default \
        --maintenance-policy=MIGRATE \
        --provisioning-model=STANDARD \
        --service-account=${SERVICE_ACCOUNT_EMAIL} \
        --scopes=https://www.googleapis.com/auth/bigquery,https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/devstorage.read_write,https://www.googleapis.com/auth/logging.write,https://www.googleapis.com/auth/monitoring.write,https://www.googleapis.com/auth/pubsub,https://www.googleapis.com/auth/service.management.readonly,https://www.googleapis.com/auth/servicecontrol,https://www.googleapis.com/auth/trace.append \
        --tags=ssh \
        --image=projects/cos-cloud/global/images/cos-stable-117-18613-75-72 \
        --boot-disk-size=10GB \
        --boot-disk-type=pd-balanced \
        --boot-disk-device-name=gcs-compressor-main \
        --container-image=mrbuk/gcs-container:0.1 \
        --container-restart-policy=always \
        --container-arg=-compressionLevel=1 \
        --container-arg=-sourceBucket=gcs-compression-source-1f34 \
        --container-arg=-destinationBucket=gcs-compression-destination-1f34 \
        --container-arg=-subscription=object-notifier-compressor \
        --container-arg=-projectId=${GCP_PROJECT_ID} \
        --no-shielded-secure-boot \
        --shielded-vtpm \
        --shielded-integrity-monitoring \
        --labels=goog-ec-src=vm_add-gcloud,container-vm=cos-stable-117-18613-75-72 \
        --threads-per-core=1
    ```

Alternativly take a look at the Terrform code in `infrastructure` to deploy.