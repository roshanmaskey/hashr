// Experimental codes for AWS HashR
package aws

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/glog"
)

var err error

type awsHashR struct {
	config aws.Config  // AWS configuration
	client *ec2.Client // AWS API client

	// Configuration parameters related to EC2 instance.
	// EC2 instance is used for attaching volumes and creating disk archive.
	sshclient        *ssh.Client // SSH client to EC2 instance
	ec2User          string      // EC2 instance username
	ec2Keyname       string      // EC2 instance SSH keyname
	ec2PublicDnsName string      // EC2 instance public FQDN or IP address
	instanceId       string      // EC2 instance where volume is attached
	region           string      // target region of the instance
}

// NewAwsHashR returns a cient of awsHashR
func NewAwsHashR() *awsHashR {
	return &awsHashR{}
}

// SetupClient setups client and loads configuration to config.
func (a *awsHashR) SetupClient() error {
	a.config, err = config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return err
	}

	a.client = ec2.NewFromConfig(a.config)

	return nil
}

func (a *awsHashR) GetRegion() string {
	return a.config.Region
}

// GetAmazonImageList returns the active AMIs owned by Amazon.
func (a *awsHashR) GetAmazonImages() ([]types.Image, error) {
	filterName := "owner-alias"
	filterValues := []string{"amazon"}
	flagFalse := false

	input := &ec2.DescribeImagesInput{
		Filters: []types.Filter{
			types.Filter{
				Name:   &filterName,
				Values: filterValues,
			},
		},
		IncludeDeprecated: &flagFalse,
		IncludeDisabled:   &flagFalse,
	}

	output, err := a.client.DescribeImages(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("Error getting image list: %v", err)
	}

	return output.Images, nil
}

// CopyImage creates a copy of AMI to HashR project and returns the new AMI id.
func (a *awsHashR) CopyImage(sourceImageId string, sourceRegion string, targetImageName string) (string, error) {
	log.Printf("Copying image %s from region %s as %s", sourceImageId, sourceRegion, targetImageName)

	input := &ec2.CopyImageInput{
		Name:          &targetImageName,
		SourceImageId: &sourceImageId,
		SourceRegion:  &sourceRegion,
	}

	output, err := a.client.CopyImage(context.TODO(), input)
	if err != nil {
		return "", fmt.Errorf("Error copying image %s: %v", sourceImageId, err)
	}

	log.Printf("Copied image %s as image ID %s", sourceImageId, *output.ImageId)

	return *output.ImageId, nil // default return
}

// DeregisterImage deletes AMI from AWS HashR project.
func (a *awsHashR) DeregisterImage(imageId string) error {
	log.Printf("Deregistering image %s", imageId)

	input := &ec2.DeregisterImageInput{
		ImageId: &imageId,
	}

	_, err := a.client.DeregisterImage(context.TODO(), input)
	if err != nil {
		fmt.Errorf("Error deregistering image %s: %v", imageId, err)
	}

	log.Printf("Deregistered image %s", imageId)
	return nil
}

// GetImageDetail returns the detail about a given image.
func (a *awsHashR) GetImageDetail(imageId string) (*types.Image, error) {
	//log.Printf("Getting details of the image %s", imageId)

	input := &ec2.DescribeImagesInput{
		ImageIds: []string{imageId},
	}

	output, err := a.client.DescribeImages(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("Error getting details of the image %s: %v", imageId, err)
	}

	if len(output.Images) != 1 {
		return nil, fmt.Errorf("Expecting 1 image, received %d images", len(output.Images))
	}

	// default return
	return &output.Images[0], nil
}

// GetSnapshot returns the detail of a specified snapshot.
func (a *awsHashR) GetSnapshot(snapshotId string) (*types.Snapshot, error) {
	log.Printf("Getting details of the snapshot %s", snapshotId)

	filterName := "snapshot-id"
	filterValues := []string{snapshotId}

	input := &ec2.DescribeSnapshotsInput{
		Filters: []types.Filter{
			types.Filter{
				Name:   &filterName,
				Values: filterValues,
			},
		},
	}

	output, err := a.client.DescribeSnapshots(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("Error getting details of the snapshot %s: %v", snapshotId, err)
	}

	if len(output.Snapshots) != 1 {
		return nil, fmt.Errorf("Expecting 1 snapshot, received %d snapshots", len(output.Snapshots))
	}

	return &output.Snapshots[0], nil
}

// GetSnapshotState returns the state of a specified snapshot.
func (a *awsHashR) GetSnapshotState(snapshotId string) (types.SnapshotState, error) {
	log.Printf("Getting state of the snapshot %s", snapshotId)

	snapshot, err := a.GetSnapshot(snapshotId)
	if err != nil {
		return types.SnapshotStateError, err
	}

	return snapshot.State, nil
}

// CreateVolume creates a volume based on the specified snapshot in the specified region.
func (a *awsHashR) CreateVolume(snapshotId string, diskSizeInGB int32, region string) (string, error) {
	log.Printf("Creating volume from snaphsot %s in the region %s", snapshotId, region)

	input := &ec2.CreateVolumeInput{
		SnapshotId:       &snapshotId,
		VolumeType:       types.VolumeTypeGp2,
		Size:             &diskSizeInGB,
		AvailabilityZone: &region,
	}

	output, err := a.client.CreateVolume(context.TODO(), input)
	if err != nil {
		return "", fmt.Errorf("Error creating a volume from the snapshot %s: %v", snapshotId, err)
	}

	log.Printf("Created the volume %s from the snapshot %s", *output.VolumeId, snapshotId)

	if err := a.waitForVolumeState(*output.VolumeId, types.VolumeStateAvailable, 600); err != nil {
		return "", err
	}

	return *output.VolumeId, nil // default
}

// DeleteVolume deletes the volume in the AWS HashR project.
func (a *awsHashR) DeleteVolume(volumeId string) error {
	log.Printf("Deleting the volume %s", volumeId)

	input := &ec2.DeleteVolumeInput{
		VolumeId: &volumeId,
	}

	_, err := a.client.DeleteVolume(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("Error deleting the volume %s: %v", volumeId, err)
	}

	log.Printf("Deleted the volume %s", volumeId)
	return nil
}

// GetVolumeDetail returns the details of the specified volume.
func (a *awsHashR) GetVolumeDetail(volumeId string) (*types.Volume, error) {
	//log.Printf("Getting details of the volume %s", volumeId)

	filterName := "volume-id"
	filterValues := []string{volumeId}

	input := &ec2.DescribeVolumesInput{
		Filters: []types.Filter{
			types.Filter{
				Name:   &filterName,
				Values: filterValues,
			},
		},
	}

	output, err := a.client.DescribeVolumes(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("Error getting details of the volume %s: %v", volumeId, err)
	}

	if len(output.Volumes) != 1 {
		return nil, fmt.Errorf("Expecting 1 volume, recevied %d volumes", len(output.Volumes))
	}

	return &output.Volumes[0], nil
}

// GetVolumeState returns the state of the volume.
func (a *awsHashR) GetVolumeState(volumeId string) (types.VolumeState, error) {
	volume, err := a.GetVolumeDetail(volumeId)
	if err != nil {
		return types.VolumeStateError, err
	}

	return volume.State, nil
}

// GetVolumeAttachment returns the attachment details related to the volume.
func (a *awsHashR) GetVolumeAttachment(volumeId string) ([]types.VolumeAttachment, error) {
	volume, err := a.GetVolumeDetail(volumeId)
	if err != nil {
		return nil, err
	}

	return volume.Attachments, nil
}

// AttachVolume attaches the specified volume to the EC2 instance.
func (a *awsHashR) AttachVolume(deviceId string, instanceId string, volumeId string) error {
	log.Printf("Attaching the volume %s (device %s) to the instance %s", volumeId, deviceId, instanceId)

	input := &ec2.AttachVolumeInput{
		Device:     &deviceId,
		InstanceId: &instanceId,
		VolumeId:   &volumeId,
	}

	output, err := a.client.AttachVolume(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("Error attaching the volume %s to the instance %s: %v", volumeId, instanceId, err)
	}

	log.Printf("Attached the volume %s to the instance %s as the device %s", volumeId, instanceId, *output.Device)

	return nil //default
}

// DetachVolume detaches the volume from the specified instance.
func (a *awsHashR) DetachVolume(deviceId string, instanceId string, volumeId string) error {
	log.Printf("Detaching the volume %s (device %s) from the instance %s", volumeId, deviceId, instanceId)

	input := &ec2.DetachVolumeInput{
		VolumeId:   &volumeId,
		Device:     &deviceId,
		InstanceId: &instanceId,
	}

	_, err := a.client.DetachVolume(context.TODO(), input)
	if err != nil {
		return fmt.Errorf("Error detaching the volume %s: %v", volumeId, err)
	}

	return nil
}

// waitForVolumeState checks for the desired state of the volume in the specified duration.
func (a *awsHashR) waitForVolumeState(volumeId string, targetState types.VolumeState, maxWaitDuration int) error {
	for i := 0; i < maxWaitDuration; i++ {
		state, err := a.GetVolumeState(volumeId)
		if err != nil {
			glog.Infof("Unabe to get the state of the volume %s: %v", volumeId, err)
			time.Sleep(1 * time.Second)
			continue
		}

		if state == targetState {
			glog.Infof("Volume %s is in the target state %s", volumeId, targetState)
			return nil
		}
	}

	return fmt.Errorf("Volume %s is not in the target state %s within %d seconds", volumeId, targetState, maxWaitDuration)
}

// waitForAttachmentState checks for the desired attachment state of the volume in the
// specified duration.
func (a *awsHashR) waitForAttachmentState(volumeId string, instanceId string, targetState types.VolumeAttachmentState, maxWaitDuration int) error {

	for i := 0; i < maxWaitDuration; i++ {
		attachments, err := a.GetVolumeAttachment(volumeId)
		if err != nil {
			glog.Errorf("Unable to get the attachment details for the volume %s: %v", volumeId, err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, attachment := range attachments {
			if attachment.State == targetState && *attachment.InstanceId == instanceId {
				glog.Infof("Volume %s is attached to the instance %s in the state %s", volumeId, instanceId, targetState)
				return nil
			}
		}
	}

	return fmt.Errorf("Volume %s did not attach to the instance %s within %d seconds", volumeId, instanceId, maxWaitDuration)
}

// SSHClientSetup sets up SSH client to the EC2 instance.
func (a *awsHashR) SSHClientSetup(user string, keyname string, server string) error {
	// Setting up SSH
	homedir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("Unable to get home directory: %v", err)
	}

	key, err := os.ReadFile(filepath.Join(homedir, ".ssh", keyname))
	if err != nil {
		return fmt.Errorf("Unable to get the SSH private key %s: %v", keyname, err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("Unable to parse the SSH private key %s: %v", keyname, err)
	}

	sshconfig := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	a.sshclient, err = ssh.Dial("tcp", fmt.Sprintf("%s:22", server), sshconfig)
	if err != nil {
		return fmt.Errorf("Unable to connect to the EC2 instance (%s): %v", server, err)
	}

	return nil // default return
}

// RunSSHCommand runs commands on remote EC2 instance.
func (a *awsHashR) RunSSHCommand(cmd string) error {
	session, err := a.sshclient.NewSession()
	if err != nil {
		return fmt.Errorf("Error creating a SSH session: %v", err)
	}
	defer session.Close()

	var buf bytes.Buffer
	session.Stdout = &buf

	if err = session.Run(cmd); err != nil {
		return fmt.Errorf("Error running command on the remote instance: %v", err)
	}

	log.Println(buf.String())

	return nil // default return
}
