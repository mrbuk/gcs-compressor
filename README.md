# gcs-compressor

`gcs-compressor` compresses objects within GCS via GZIP. Similar to `gzip` it deletes the source file after successful compression.

You can run `gcs-compressor` in two modes:

1. interactive  - compress a specific object (your provide source and destination)
2. event-driven - using a Cloud Storage Notification via PubSub (source is coming via PubSub)

The application is written in Go and can either be run 

as a binary
    
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
        -subscription object-notifier-compressor \
        -topic object-notifier \
        -projectId dev-demo-333610 


or via Docker

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
        -subscription object-notifier-compressor \
        -topic object-notifier \
        -projectId dev-demo-333610 

**Important:** PubSub Messages are acknowledged right before the compression operation starts. 
This is due to the fact that compressing a single file can take longer than the current existing ACK Deadline.
In case SIGINT / SIGTERM is send to the process workers are canceled gracefully and all messages that have been in fligth are republished and can be reprocessed. 
In case errors appear such messages are not handles and need to be processed manually (e.g. either re-sending a event into PubSub or running it in mode 1 - interactive).

## Permissions

`gcs-compressor` requires following permissions
  - PubSub Subscriber (on the subscription)
  - Storage Object User (on source and destination bucket)

# Runtime environment

It is possible to have `gcs-compressor` be run as a Cloud Function. Due to the fact the a compression operation can take long than 540s or 900s it is not recommended.

Instead the recommendation is to run `gcs-compressor` via a Container directly in Google Compute Engine (GCE) via Google Container OS. GCE allows also for optmizions e.g.

- custom instance types with a few RAM as possible (e.g. `n2-custom-16-8192`)
- disabling HyperThreading (e.g. `n2-custom-16-8192` shows as 8 cores instead of 16)

To deploy please the Terrform code in `infrastructure` to deploy. It requires:
- source bucket
- destination bucket
- network
to exist.
