package s3

import (
	"context"
	"deployer-agent/config"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

var (
	ErrNotConfigured = errors.New("s3 is not configured on this agent")
	ErrNotFound      = errors.New("s3 object not found")
)

type Client struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

var sharedClient *Client

func Init(cfg *config.S3Config) error {
	if !cfg.IsConfigured() {
		sharedClient = nil
		return nil
	}

	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if service == s3.ServiceID && strings.TrimSpace(cfg.Endpoint) != "" {
			return aws.Endpoint{URL: cfg.Endpoint, SigningRegion: cfg.Region, HostnameImmutable: true}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
		awsconfig.WithEndpointResolverWithOptions(resolver),
	)
	if err != nil {
		return err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
	})

	sharedClient = &Client{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.Bucket,
	}
	return nil
}

func GetClient() *Client {
	return sharedClient
}

func IsConfigured() bool {
	return sharedClient != nil
}

func (c *Client) Bucket() string {
	return c.bucket
}

func (c *Client) PresignPutObject(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	if expires == 0 {
		expires = 15 * time.Minute
	}
	out, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, func(o *s3.PresignOptions) {
		o.Expires = expires
	})
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (c *Client) PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	if expires == 0 {
		expires = 15 * time.Minute
	}
	out, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expires
	})
	if err != nil {
		return "", err
	}
	return out.URL, nil
}

func (c *Client) HeadObject(ctx context.Context, key string) (int64, error) {
	out, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundErr(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if out.ContentLength == nil {
		return 0, nil
	}
	return *out.ContentLength, nil
}

func (c *Client) DeleteObject(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFoundErr(err) {
			return nil
		}
		return err
	}
	return nil
}

func isNotFoundErr(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NotFound" || code == "NoSuchKey" || code == "404"
	}
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	return false
}
