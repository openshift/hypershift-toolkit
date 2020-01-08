package aws

import (
	"fmt"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

type LBInfo struct {
	VPC    string
	Zone   string
	Subnet string
}

type AWSHelper struct {
	elbClient     *elbv2.ELBV2
	ec2Client     *ec2.EC2
	route53Client *route53.Route53
	s3Client      *s3.S3
	s3Uploader    *s3manager.Uploader
	infraName     string
}

// NewAWSHelper creates an instance of the AWS helper with clients for each of the required services
func NewAWSHelper(key string, secret string, region string, infraName string) (*AWSHelper, error) {
	awsConfig := &aws.Config{
		Region:      aws.String(region),
		Credentials: credentials.NewStaticCredentials(key, secret, ""),
	}
	s, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}
	return &AWSHelper{
		elbClient:     elbv2.New(s),
		ec2Client:     ec2.New(s),
		route53Client: route53.New(s),
		s3Client:      s3.New(s),
		s3Uploader:    s3manager.NewUploader(s),
		infraName:     infraName,
	}, nil
}

// LoadBalancerInfo returns load balancer information for one of the zones that
// contains worker machines
func (h *AWSHelper) LoadBalancerInfo(machineNames []string) (*LBInfo, error) {
	result := &LBInfo{}
	output, err := h.elbClient.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String(h.infraName + "-ext")},
	})
	if err != nil {
		return nil, err
	}
	if len(output.LoadBalancers) == 0 {
		return nil, fmt.Errorf("no load balancers found")
	}
	lb := output.LoadBalancers[0]
	result.VPC = aws.StringValue(lb.VpcId)

	found := false
	for _, az := range lb.AvailabilityZones {
		zoneName := aws.StringValue(az.ZoneName)
		for _, m := range machineNames {
			if strings.HasPrefix(m, fmt.Sprintf("%s-worker-%s", h.infraName, zoneName)) {
				found = true
				result.Zone = zoneName
				result.Subnet = aws.StringValue(az.SubnetId)
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("cannot find a suitable zone with workers in it")
	}
	return result, nil
}

// EnsureEIP ensures that an EIP is allocated with the given name
func (h *AWSHelper) EnsureEIP(name string) (string, string, error) {
	allocID := ""
	addressIP := ""
	output, err := h.ec2Client.DescribeAddresses(&ec2.DescribeAddressesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: []*string{aws.String(name)},
			},
		},
	})
	if err != nil {
		return "", "", err
	}
	if len(output.Addresses) > 0 {
		allocID = aws.StringValue(output.Addresses[0].AllocationId)
		addressIP = aws.StringValue(output.Addresses[0].PublicIp)
		return allocID, addressIP, nil
	}
	allocateOutput, err := h.ec2Client.AllocateAddress(&ec2.AllocateAddressInput{Domain: aws.String("vpc")})
	if err != nil {
		return "", "", err
	}
	allocID = aws.StringValue(allocateOutput.AllocationId)
	addressIP = aws.StringValue(allocateOutput.PublicIp)
	_, err = h.ec2Client.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{aws.String(allocID)},
		Tags: []*ec2.Tag{
			{
				Key:   aws.String("Name"),
				Value: aws.String(name),
			},
			ownedTag(h.infraName),
		},
	})
	if err != nil {
		return "", "", err
	}

	return allocID, addressIP, nil
}

func (h *AWSHelper) RemoveEIP(name string) error {
	notFound := false
	allocationID := ""
	err := wait.PollImmediate(15*time.Second, 4*time.Minute, func() (bool, error) {
		output, err := h.ec2Client.DescribeAddresses(&ec2.DescribeAddressesInput{
			Filters: []*ec2.Filter{
				{
					Name:   aws.String("tag:Name"),
					Values: []*string{aws.String(name)},
				},
			},
		})
		if err != nil {
			return false, err
		}
		if len(output.Addresses) == 0 {
			notFound = true
			return true, nil
		}
		address := output.Addresses[0]
		allocationID = aws.StringValue(address.AllocationId)
		if address.NetworkInterfaceId == nil || aws.StringValue(address.NetworkInterfaceId) == "" {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}
	if notFound {
		return nil
	}
	if allocationID == "" {
		return fmt.Errorf("did not find allocation ID for EIP %s", name)
	}
	_, err = h.ec2Client.ReleaseAddress(&ec2.ReleaseAddressInput{
		AllocationId: aws.String(allocationID),
	})
	return err
}

// EnsureNLB ensures that a network load balancer exists with the given subnet. If an EIP allocation
// ID is passed, it assigns it to the NLB subnet mappings.
func (h *AWSHelper) EnsureNLB(nlbName, subnet, eipAllocID string) (string, string, error) {
	output, err := h.elbClient.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String(nlbName)},
	})
	notFound := false
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == elbv2.ErrCodeLoadBalancerNotFoundException {
				notFound = true
			}
		}
		if !notFound {
			return "", "", err
		}
	}
	if !notFound && len(output.LoadBalancers) > 0 {
		lb := output.LoadBalancers[0]
		return aws.StringValue(lb.LoadBalancerArn), aws.StringValue(lb.DNSName), nil
	}

	input := &elbv2.CreateLoadBalancerInput{
		Name:   aws.String(nlbName),
		Scheme: aws.String(elbv2.LoadBalancerSchemeEnumInternetFacing),
		Type:   aws.String(elbv2.LoadBalancerTypeEnumNetwork),
		Tags: []*elbv2.Tag{
			ownedLBTag(h.infraName),
		},
	}
	if len(eipAllocID) > 0 {
		input.SubnetMappings = []*elbv2.SubnetMapping{
			{
				SubnetId:     aws.String(subnet),
				AllocationId: aws.String(eipAllocID),
			},
		}
	} else {
		input.Subnets = []*string{aws.String(subnet)}
	}
	nlbResult, err := h.elbClient.CreateLoadBalancer(input)
	if err != nil {
		return "", "", err
	}
	lb := nlbResult.LoadBalancers[0]
	return aws.StringValue(lb.LoadBalancerArn), aws.StringValue(lb.DNSName), nil
}

// RemoveNLB removes an existing load balancer
func (h *AWSHelper) RemoveNLB(nlbName string) error {
	output, err := h.elbClient.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{
		Names: []*string{aws.String(nlbName)},
	})
	notFound := false
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == elbv2.ErrCodeLoadBalancerNotFoundException {
				notFound = true
			}
		}
		if notFound {
			return nil
		}
		return err
	}
	arn := aws.StringValue(output.LoadBalancers[0].LoadBalancerArn)
	_, err = h.elbClient.DeleteLoadBalancer(&elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: &arn,
	})
	return err
}

// EnsureTargetGroup ensures that a target group with the given name and port exists
func (h *AWSHelper) EnsureTargetGroup(vpc, tgName string, port int) (string, error) {
	output, err := h.elbClient.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		Names: []*string{aws.String(tgName)},
	})
	notFound := false
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == elbv2.ErrCodeTargetGroupNotFoundException {
				notFound = true
			}
		}
		if !notFound {
			return "", err
		}
	}
	if !notFound && len(output.TargetGroups) > 0 {
		tg := output.TargetGroups[0]
		if aws.Int64Value(tg.Port) == int64(port) {
			return aws.StringValue(tg.TargetGroupArn), nil
		}
		_, err := h.elbClient.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
			TargetGroupArn: tg.TargetGroupArn,
		})
		if err != nil {
			return "", err
		}
	}
	tgResult, err := h.elbClient.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:                       aws.String(tgName),
		Port:                       aws.Int64(int64(port)),
		VpcId:                      aws.String(vpc),
		Protocol:                   aws.String(elbv2.ProtocolEnumTcp),
		TargetType:                 aws.String(elbv2.TargetTypeEnumIp),
		HealthCheckProtocol:        aws.String(elbv2.ProtocolEnumTcp),
		HealthCheckEnabled:         aws.Bool(true),
		HealthCheckIntervalSeconds: aws.Int64(10),
		HealthCheckTimeoutSeconds:  aws.Int64(10),
		HealthyThresholdCount:      aws.Int64(2),
		UnhealthyThresholdCount:    aws.Int64(2),
	})
	if err != nil {
		return "", err
	}
	return aws.StringValue(tgResult.TargetGroups[0].TargetGroupArn), nil
}

func (h *AWSHelper) EnsureTarget(targetGroupARN string, targetID string) error {
	output, err := h.elbClient.DescribeTargetHealth(&elbv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(targetGroupARN),
	})
	if err != nil {
		return err
	}
	for _, hd := range output.TargetHealthDescriptions {
		if aws.StringValue(hd.Target.Id) == targetID {
			return nil
		}
		_, err := h.elbClient.DeregisterTargets(&elbv2.DeregisterTargetsInput{
			TargetGroupArn: aws.String(targetGroupARN),
			Targets:        []*elbv2.TargetDescription{{Id: hd.Target.Id}},
		})
		if err != nil {
			return err
		}
	}
	_, err = h.elbClient.RegisterTargets(&elbv2.RegisterTargetsInput{
		TargetGroupArn: aws.String(targetGroupARN),
		Targets: []*elbv2.TargetDescription{
			{
				Id: aws.String(targetID),
			},
		},
	})
	return err
}

// RemoveTargetGroup removes a target group by name
func (h *AWSHelper) RemoveTargetGroup(tgName string) error {
	output, err := h.elbClient.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		Names: []*string{aws.String(tgName)},
	})
	notFound := false
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == elbv2.ErrCodeTargetGroupNotFoundException {
				notFound = true
			}
		}
		if notFound {
			return nil
		}
		return err
	}
	tgARN := aws.StringValue(output.TargetGroups[0].TargetGroupArn)
	_, err = h.elbClient.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(tgARN),
	})
	return err
}

// EnsureUDPTargetGroup ensures that a UDP target group exists with the given port and healt check port
func (h *AWSHelper) EnsureUDPTargetGroup(vpc, tgName string, port, healthCheckPort int) (string, error) {
	output, err := h.elbClient.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{
		Names: []*string{aws.String(tgName)},
	})
	notFound := false
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok {
			if awsErr.Code() == elbv2.ErrCodeTargetGroupNotFoundException {
				notFound = true
			}
		}
		if !notFound {
			return "", err
		}
	}
	if !notFound && len(output.TargetGroups) > 0 {
		tg := output.TargetGroups[0]
		if aws.Int64Value(tg.Port) == int64(port) {
			return aws.StringValue(tg.TargetGroupArn), nil
		}
		_, err := h.elbClient.DeleteTargetGroup(&elbv2.DeleteTargetGroupInput{
			TargetGroupArn: tg.TargetGroupArn,
		})
		if err != nil {
			return "", err
		}
	}
	tgResult, err := h.elbClient.CreateTargetGroup(&elbv2.CreateTargetGroupInput{
		Name:                       aws.String(tgName),
		Protocol:                   aws.String("UDP"),
		Port:                       aws.Int64(int64(port)),
		VpcId:                      aws.String(vpc),
		TargetType:                 aws.String(elbv2.TargetTypeEnumInstance),
		HealthCheckProtocol:        aws.String(elbv2.ProtocolEnumTcp),
		HealthCheckPort:            aws.String(fmt.Sprintf("%d", healthCheckPort)),
		HealthCheckEnabled:         aws.Bool(true),
		HealthCheckIntervalSeconds: aws.Int64(10),
		HealthCheckTimeoutSeconds:  aws.Int64(10),
		HealthyThresholdCount:      aws.Int64(2),
		UnhealthyThresholdCount:    aws.Int64(2),
	})
	if err != nil {
		return "", err
	}
	return aws.StringValue(tgResult.TargetGroups[0].TargetGroupArn), nil
}

func (h *AWSHelper) EnsureListener(lbARN, tgARN string, port int, udp bool) error {
	listeners, err := h.elbClient.DescribeListeners(&elbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		return err
	}
	for _, listener := range listeners.Listeners {
		if aws.Int64Value(listener.Port) != int64(port) {
			continue
		}
		if len(listener.DefaultActions) > 0 && aws.StringValue(listener.DefaultActions[0].TargetGroupArn) == tgARN {
			return nil
		}
		_, err = h.elbClient.DeleteListener(&elbv2.DeleteListenerInput{
			ListenerArn: listener.ListenerArn,
		})
		if err != nil {
			return err
		}
	}
	protocol := elbv2.ProtocolEnumTcp
	if udp {
		protocol = "UDP"
	}
	_, err = h.elbClient.CreateListener(&elbv2.CreateListenerInput{
		Port:            aws.Int64(int64(port)),
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        aws.String(protocol),
		DefaultActions: []*elbv2.Action{
			{
				TargetGroupArn: aws.String(tgARN),
				Type:           aws.String(elbv2.ActionTypeEnumForward),
			},
		},
	})
	return err
}

func (h *AWSHelper) EnsureCNameRecord(zoneID, dnsName, targetName string) error {
	_, err := h.route53Client.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String("UPSERT"),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(dnsName),
						TTL:  aws.Int64(30),
						Type: aws.String("CNAME"),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(targetName),
							},
						},
					},
				},
			},
		},
	})
	return err
}

func (h *AWSHelper) RemoveCNameRecord(zoneID, dnsName string) error {
	value := ""
	err := h.route53Client.ListResourceRecordSetsPages(&route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	}, func(output *route53.ListResourceRecordSetsOutput, lastPage bool) bool {
		for _, r := range output.ResourceRecordSets {
			if aws.StringValue(r.Name) == dnsName {
				if len(r.ResourceRecords) > 0 {
					value = aws.StringValue(r.ResourceRecords[0].Value)
					return false
				}
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	_, err = h.route53Client.ChangeResourceRecordSets(&route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &route53.ChangeBatch{
			Changes: []*route53.Change{
				{
					Action: aws.String("DELETE"),
					ResourceRecordSet: &route53.ResourceRecordSet{
						Name: aws.String(dnsName),
						Type: aws.String("CNAME"),
						TTL:  aws.Int64(30),
						ResourceRecords: []*route53.ResourceRecord{
							{
								Value: aws.String(value),
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	return nil
}

func (h *AWSHelper) EnsureWorkersAllowNodePortAccess() error {
	result, err := h.ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: []*string{aws.String(fmt.Sprintf("%s-worker-sg", h.infraName))},
			},
		},
	})
	if err != nil {
		return err
	}
	if len(result.SecurityGroups) == 0 {
		return fmt.Errorf("could not find the workers security group")
	}
	sg := result.SecurityGroups[0]
	foundTCPRule := false
	foundUDPRule := false
	for _, permission := range sg.IpPermissions {
		if aws.Int64Value(permission.FromPort) == 30000 && aws.Int64Value(permission.ToPort) == 32767 {
			if aws.StringValue(permission.IpProtocol) == "tcp" {
				for _, ipRange := range permission.IpRanges {
					if aws.StringValue(ipRange.CidrIp) == "10.0.0.0/16" {
						foundTCPRule = true
						break
					}
				}
			}
			if aws.StringValue(permission.IpProtocol) == "udp" {
				for _, ipRange := range permission.IpRanges {
					if aws.StringValue(ipRange.CidrIp) == "0.0.0.0/0" {
						foundUDPRule = true
						break
					}
				}
			}
		}
		if foundTCPRule && foundUDPRule {
			break
		}
	}
	if !foundTCPRule {
		_, err := h.ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:    sg.GroupId,
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			CidrIp:     aws.String("10.0.0.0/16"),
		})
		if err != nil {
			return err
		}
	}
	if !foundUDPRule {
		_, err := h.ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:    sg.GroupId,
			IpProtocol: aws.String("udp"),
			FromPort:   aws.Int64(30000),
			ToPort:     aws.Int64(32767),
			CidrIp:     aws.String("0.0.0.0/0"),
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// EnsureIgnitionBucket ensures that a bucket with the given name exists and that it contains
// a file with the contents of the ignition filename passed.
func (h *AWSHelper) EnsureIgnitionBucket(name, fileName string) error {
	_, err := h.s3Client.GetBucketLocation(&s3.GetBucketLocationInput{
		Bucket: aws.String(name),
	})
	if err != nil {
		// Bucket likely doesn't exist, create it
		_, err = h.s3Client.CreateBucket(&s3.CreateBucketInput{
			Bucket: aws.String(name),
			ACL:    aws.String("public-read"),
		})
		if err != nil {
			return fmt.Errorf("failed to create bucket %s: %v", name, err)
		}
	}
	_, err = h.s3Client.PutBucketTagging(&s3.PutBucketTaggingInput{
		Bucket: aws.String(name),
		Tagging: &s3.Tagging{
			TagSet: []*s3.Tag{
				{
					Key:   aws.String(fmt.Sprintf("kubernetes/cluster/%s", h.infraName)),
					Value: aws.String("owned"),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to tag bucket %s: %v", name, err)
	}
	ign, err := os.Open(fileName)
	if err != nil {
		return fmt.Errorf("cannot open ignition file %s: %v", fileName, err)
	}
	defer ign.Close()
	_, err = h.s3Uploader.Upload(&s3manager.UploadInput{
		ACL:    aws.String("public-read"),
		Bucket: aws.String(name),
		Key:    aws.String("worker.ign"),
		Body:   ign,
	})
	if err != nil {
		return fmt.Errorf("failed to upload ignition file: %v", err)
	}
	return nil
}

func (h *AWSHelper) RemoveIgnitionBucket(name string) error {
	var deleteErr error
	_, err := h.s3Client.GetBucketLocation(&s3.GetBucketLocationInput{
		Bucket: aws.String(name),
	})
	if awsErr, ok := err.(awserr.Error); ok {
		if awsErr.Code() == s3.ErrCodeNoSuchBucket {
			return nil
		}
	}
	if err != nil {
		return err
	}

	err = h.s3Client.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(name),
	}, func(output *s3.ListObjectsV2Output, last bool) bool {
		for _, obj := range output.Contents {
			_, deleteErr = h.s3Client.DeleteObject(&s3.DeleteObjectInput{
				Bucket: aws.String(name),
				Key:    obj.Key,
			})
			if deleteErr != nil {
				return false
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	if deleteErr != nil {
		return deleteErr
	}
	_, err = h.s3Client.DeleteBucket(&s3.DeleteBucketInput{
		Bucket: aws.String(name),
	})
	return err

}

func ownedTag(infraName string) *ec2.Tag {
	return &ec2.Tag{
		Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraName)),
		Value: aws.String("owned"),
	}
}

func ownedLBTag(infraName string) *elbv2.Tag {
	return &elbv2.Tag{
		Key:   aws.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraName)),
		Value: aws.String("owned"),
	}
}
