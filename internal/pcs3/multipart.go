package pcs3

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CreateMultipartUpload инициализирует начало составной загрузки и возвращает UploadId.
func CreateMultipartUpload(ctx context.Context, client *s3.Client, bucketName, key string) (string, error) {
	output, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(output.UploadId), nil
}

// UploadPart загружает отдельную часть объекта. Возвращает ETag загруженной части.
func UploadPart(ctx context.Context, client *s3.Client, bucketName, key, uploadId string, partNumber int32, body io.Reader) (string, error) {
	output, err := client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucketName),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadId),
		PartNumber: aws.Int32(partNumber),
		Body:       body,
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(output.ETag), nil
}

// ListParts возвращает список уже загруженных частей для указанной загрузки.
func ListParts(ctx context.Context, client *s3.Client, bucketName, key, uploadId string) ([]types.Part, error) {
	output, err := client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
	})
	if err != nil {
		return nil, err
	}
	return output.Parts, nil
}

// CompleteMultipartUpload завершает загрузку, собирая переданные части (PartNumber + ETag) в единый объект.
func CompleteMultipartUpload(ctx context.Context, client *s3.Client, bucketName, key, uploadId string, parts []types.CompletedPart) error {
	_, err := client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	return err
}

// ListMultipartUploads выводит список всех незавершенных составных загрузок в бакете.
func ListMultipartUploads(ctx context.Context, client *s3.Client, bucketName string) ([]types.MultipartUpload, error) {
	output, err := client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return nil, err
	}
	return output.Uploads, nil
}

// AbortMultipartUpload прерывает загрузку и удаляет все ранее загруженные части.
func AbortMultipartUpload(ctx context.Context, client *s3.Client, bucketName, key, uploadId string) error {
	_, err := client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucketName),
		Key:      aws.String(key),
		UploadId: aws.String(uploadId),
	})
	return err
}
