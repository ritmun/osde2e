package aws

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/awsdocs/aws-doc-sdk-examples/gov2/s3/actions"
)

const velerosubstr = "managed-velero"

// ReadFromS3Session reads a key from S3 using given AWS context.
func ReadFromS3Session(  inputKey string) ([]byte, error) {
	bucket, key, err := ParseS3URL(inputKey)
	if err != nil {
		return nil, fmt.Errorf("error trying to parse S3 URL: %v", err)
	}
	basics := actions.BucketBasics{S3Client: CcsAwsSession.s3}
	if err != nil {
		return nil,err
	}
	result, err := basics.S3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
	})
	if err != nil {
		log.Printf("Couldn't get object %v:%v. Here's why: %v\n", bucket, key, err)
		return nil,err
	}
	defer result.Body.Close()
	 
	body, err := io.ReadAll(result.Body)
	if err != nil {
		log.Printf("Couldn't read object body to %v from %v. Here's why: %v\n",bucket, key, err)
	}
 
	return body, nil
}

// WriteToS3Session writes the given byte array to S3.
func WriteToS3Session( outputKey string, data []byte) {
	bucket, key, err := ParseS3URL(outputKey)
	if err != nil {
		log.Printf("error trying to parse S3 URL %s: %v", outputKey, err)
		return
	}
	basics := actions.BucketBasics{S3Client: CcsAwsSession.s3}
		_, err = basics.S3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body: bytes.NewReader(data),
		})
		if err != nil {
			log.Printf("Couldn't upload to %v:%v. Here's why: %v\n",
				  bucket, key, err)
		}
	log.Printf("Uploaded to %s", outputKey)
	return
}

// CreateS3URL creates an S3 URL from a bucket and a key string.
func CreateS3URL(bucket string, keys ...string) string {
	strippedBucket := strings.Trim(bucket, "/")

	strippedKeys := make([]string, len(keys))
	for i, key := range keys {
		strippedKeys[i] = strings.Trim(key, "/")
	}

	s3JoinArray := []string{"s3:/", strippedBucket}
	s3JoinArray = append(s3JoinArray, strippedKeys...)

	return strings.Join(s3JoinArray, "/")
}

// ParseS3URL parses an S3 url into a bucket and key.
func ParseS3URL(s3URL string) (string, string, error) {
	parsedURL, err := url.Parse(s3URL)
	if err != nil {
		return "", "", err
	}

	return parsedURL.Host, parsedURL.Path, nil
}

// CleanupS3Buckets finds buckets with substring "osde2e-" or "managed-velero",
// older than given duration, then deletes bucket objects and then buckets
func (CcsAwsSession *ccsAwsSession) CleanupS3Buckets(olderthan time.Duration, dryrun bool) error {
	err := CcsAwsSession.GetAWSSessions()
 	bucketBasics := actions.BucketBasics{S3Client: CcsAwsSession.s3}
	if err != nil {
		return err
	}

	buckets, err := bucketBasics.ListBuckets()
	if err != nil {
		return err
	}
	for _, bucket := range buckets {
		if (strings.Contains(*bucket.Name, rolesubstr) || strings.Contains(*bucket.Name, velerosubstr)) && time.Since(*bucket.CreationDate) > olderthan {
			fmt.Printf("Bucket will be deleted: %s\n", bucket)
			if !dryrun {
				objects, err := bucketBasics.ListObjects(*bucket.Name)
				var objKeys []string
				for _, object := range objects {
					objKeys = append(objKeys, *object.Key)
					log.Printf("\t%v\n", *object.Key)
				}
				err = bucketBasics.DeleteObjects(*bucket.Name, objKeys)
				if err != nil {
					fmt.Printf("error deleting objects from bucket %s, skipping: %s", *bucket.Name, err)
					continue
				}
				fmt.Println("Deleted object(s) from bucket")
				err = bucketBasics.DeleteBucket(*bucket.Name)
				if  err != nil {
					fmt.Printf("error deleting bucket: %s: %s", *bucket.Name, err)
					continue
				}
				fmt.Println("Deleted bucket")
			}
		}
	}

	return nil
}

 
