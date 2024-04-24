package aws

import (
	"context"
	"fmt"
	"log"
	"sync"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	viper "github.com/openshift/osde2e/pkg/common/concurrentviper"
	"github.com/openshift/osde2e/pkg/common/config"
)

type ccsAwsSession struct {
	config awsconfig.Config
	accountId string
	iam       *iam.Client
	s3        *s3.Client
 
	ec2       *ec2.Client
	once      sync.Once
}

// CcsAwsSession is the global AWS session for interacting with AWS.
var CcsAwsSession ccsAwsSession

// GetAWSSessions returns a new AWS type with the first AWS account in the config file. The session is cached for the rest of the program.
func (CcsAwsSession *ccsAwsSession) GetAWSSessions() error {
	 
	CcsAwsSession.once.Do(func() {
		// LoadDefaultConfig uses default credential chain to find AWS credentials in the following order:
		// 1. Environment variables 2. Shared configuration files 3. IAM role for ECS task 4. IAM role for EC2 instance
		cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
			awsconfig.WithRegion(viper.GetString(config.AWSRegion)),
			awsconfig.WithSharedConfigProfile(viper.GetString(config.AWSProfile)),
		)
		 
		if err != nil {
			log.Printf("error initializing AWS session: %v", err)
		}
	  
		CcsAwsSession.config = cfg
		CcsAwsSession.iam = iam.NewFromConfig(cfg)
		CcsAwsSession.s3 = s3.NewFromConfig(cfg)
		CcsAwsSession.ec2 = ec2.NewFromConfig(cfg)
		CcsAwsSession.accountId = viper.GetString(config.AWSAccountId)
 	 
	})

	return nil
}


// GetCredentials returns the credentials for the current aws session
func (CcsAwsSession *ccsAwsSession) GetCredentials() ( *awsv2.Credentials, error) {
 
	creds, err := CcsAwsSession.ec2.Options().Credentials.Retrieve(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to get aws credentials: %v", err)
	}
	return &creds, nil
}

// GetRegion returns the region set when the session was created
func (CcsAwsSession *ccsAwsSession) GetRegion() string {
	return CcsAwsSession.iam.Options().Region
}

// GetAccountId returns the aws account id in session
func (CcsAwsSession *ccsAwsSession) GetAccountId() string {
	return CcsAwsSession.accountId
}
