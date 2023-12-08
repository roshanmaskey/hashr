/*
Copyright 2023 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/google/hashr/core/hashr"
)

const (
	// RepoName contains the repository name.
	RepoName = "aws"
)

var ahashr *awsHashR

type AwsImage struct {
	imageId         string       // AMI in HashR project
	image           *types.Image // Image in HashR project
	sourceImageId   string       // AMI owned by AWS
	sourceImage     *types.Image // Source Image owned by Amazon
	deviceName      string       // Device name in EC2
	archiveName     string       // Disk archive name
	volumeId        string       // Volume ID of the image
	maxWaitDuration int          // Maximum time waiting for state to be available
	localPath       string
	remotePath      string
	bucketName      string
	quickSha256hash string
}

func NewAwsImage() *AwsImage {
	return &AwsImage{}
}

// ID returns the unique AMI in HashR project.
func (a *AwsImage) ID() string {
	return a.imageId
}

// SourceID returns the unique AMI of the source owned by Amazon.
func (a *AwsImage) SourceID() string {
	return a.sourceImageId
}

// RepoName returns the AWS
func (a *AwsImage) RepoName() string {
	return RepoName
}

// RepoPath returns the repository path.
func (a *AwsImage) RepoPath() string {
	if a.sourceImage != nil {
		return *a.sourceImage.ImageLocation
	}

	return ""
}

// LocalPath returns the image local path.
func (a *AwsImage) LocalPath() string {
	return a.localPath
}

// RemotePath returns the image remote path.
func (a *AwsImage) RemotePath() string {
	return a.remotePath
}

// QuickSHA256Hash calculates and returns the SHA256 hash of the image attributes.
func (a *AwsImage) QuickSHA256Hash() (string, error) {
	// Check if the quick hash was already calculated.
	if a.quickSha256hash != "" {
		return a.quickSha256hash, nil
	}

	// We need to use sourceImage to calculate the quick hash that is owned
	// by Amazon.

	// The source Image should already exist. In case it doesn't exist, we need
	// to get the details.
	if a.sourceImage == nil {
		if a.sourceImageId == "" {
			return "", fmt.Errorf("source image ID is empty and source image object is nil")
		}

		// If we have the source image ID, we can get the image details.
		if ahashr == nil {
			return "", fmt.Errorf("awsHashR object is not initialized")
		}

		image, err := ahashr.GetImageDetail(a.sourceImageId)
		if err != nil {
			return "", fmt.Errorf("error getting details of the source AMI %s: %v", a.sourceImageId, err)
		}

		a.sourceImage = image
	}

	data := [][]byte{
		[]byte(*a.sourceImage.ImageId),
		[]byte(*a.sourceImage.CreationDate),
		[]byte(*a.sourceImage.DeprecationTime),
	}

	var hashBytes []byte

	for _, bytes := range data {
		hashBytes = append(hashBytes, bytes...)
	}

	a.quickSha256hash = fmt.Sprintf("%x", sha256.Sum256(hashBytes))

	return a.quickSha256hash, nil
}

// Description returns the image description.
func (a *AwsImage) Description() string {
	if a.image.Description != nil {
		return *a.image.Description
	}

	return ""
}

///
/// Repo
///

type Repo struct {
	osName          string      // Repo filtered by OS name
	osArchs         []string    // Repo filtered by OS architectures
	instanceId      string      // EC2 instance
	maxWaitDuration int         // Maximum wait duration
	bucketName      string      // S3 bucket of the AWS HashR project
	localPath       string      // Local directory where archives will be downloaded
	remotePath      string      // Remote directory in EC2 instance where archive will be saved
	images          []*AwsImage // Source images owned by Amazon
}

// NewRepo returns a new AWS repo.
func NewRepo(ctx context.Context, instanceId string, osName string, osArchs []string, maxWaitDuration int, bucketName string, localPath string, remotePath string, user string) (*Repo, error) {
	// Setup awsHashR object ahashr
	ahashr = NewAwsHashR()
	if err := ahashr.SetupClient(instanceId); err != nil {
		log.Fatal(err)
	}

	ahashr.instanceId = instanceId
	ahashr.ec2User = user

	// Setting up SSH client
	if err := ahashr.SSHClientSetup(ahashr.ec2User, ahashr.ec2Keyname, ahashr.ec2PublicDnsName); err != nil {
		log.Fatal(err)
	}

	return &Repo{
		osName:          osName,
		osArchs:         osArchs,
		instanceId:      instanceId,
		maxWaitDuration: maxWaitDuration,
		bucketName:      bucketName,
		localPath:       localPath,
		remotePath:      remotePath,
	}, nil
}

// RepoName returns the AWS repository name.
func (r *Repo) RepoName() string {
	return RepoName
}

// RepoPath returns the path of the repository.
func (r *Repo) RepoPath() string {
	return ""
}

// DiscoverRepo traverses the repository and looks for the AMIs.
func (r *Repo) DiscoverRepo() ([]hashr.Source, error) {
	var sources []hashr.Source

	images, err := ahashr.GetAmazonImages(r.osName)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("image details is missing or nil for OS %s", r.osName)
	}

	for _, image := range images {
		awsimage := &AwsImage{
			sourceImageId:   *image.ImageId,
			sourceImage:     &image,
			archiveName:     fmt.Sprintf("%s-raw.dd.gz", *image.ImageId),
			maxWaitDuration: r.maxWaitDuration,
			bucketName:      r.bucketName,
			localPath:       r.localPath,
			remotePath:      r.remotePath,
		}

		r.images = append(r.images, awsimage)
		sources = append(sources, awsimage)
	}

	return sources, nil
}

// Preprocess extracts the content of the image.
func (a *AwsImage) Preprocess() (string, error) {
	if err := a.copy(); err != nil {
		return "", fmt.Errorf("error copying image %s to HashR project: %v", a.sourceImageId, err)
	}

	if err := a.generate(); err != nil {
		return "", fmt.Errorf("error generating disk archive of the image %s: %v", a.sourceImageId, err)
	}

	if err := a.download(); err != nil {
		return "", fmt.Errorf("error downloading disk archive %s from S3 bucket %s: %v", a.archiveName, a.bucketName, err)
	}

	// deleteBucketArchive
	// Determine if we want to keep or delete the archive in HashR AWS S3 bucket.
	//
	// Current plan is to leave the archive on AWS S3 bucket.
	// AWS HashR users should manually delete the archives from the S3 bucket.
	//
	// TODO (roshan): Users should provide the value of deleteBucketArchive
	deleteBucketArchive := false

	if err := a.cleanup(deleteBucketArchive); err != nil {
		return "", fmt.Errorf("error deleting the archive %s from S3 bucket %s: %v", a.archiveName, a.bucketName, err)
	}

	return "", nil // default
}

func (a *AwsImage) copy() error {
	// Source image and ID is required
	if a.sourceImageId == "" {
		return fmt.Errorf("source AMI does not exist")
	}

	if a.sourceImage == nil {
		return fmt.Errorf("source image does not exist")
	}

	sourceRegion, err := ahashr.GetAvailabilityZoneRegion()
	if err != nil {
		return err
	}

	targetImageName := fmt.Sprintf("copy-%s", a.sourceImageId)

	imageId, err := ahashr.CopyImage(a.sourceImageId, sourceRegion, targetImageName)
	if err != nil {
		return err
	}
	a.imageId = imageId

	time.Sleep(10 * time.Second)

	for i := 0; i < a.maxWaitDuration; i++ {
		time.Sleep(2 * time.Second)

		image, err := ahashr.GetImageDetail(a.imageId)
		if err != nil {
			return err
		}

		if image.State == types.ImageStateAvailable {
			a.image = image
			break
		}
	}

	if a.image == nil {
		return fmt.Errorf("unable to get image details for image ID %s", imageId)
	}

	log.Printf("Image - Image %s (%s) is ready for processing", *a.image.ImageId, a.image.State)

	return nil
}

func (a *AwsImage) generate() error {
	var snapshotIds []string

	for _, blockdevice := range a.image.BlockDeviceMappings {
		snapshotIds = append(snapshotIds, *blockdevice.Ebs.SnapshotId)
	}

	if len(snapshotIds) == 0 {
		return fmt.Errorf("no snapshots in the image %s", a.imageId)
	}
	snapshotId := snapshotIds[0]
	volumeSize := int32(*a.image.BlockDeviceMappings[0].Ebs.VolumeSize)

	if len(snapshotIds) > 1 {
		log.Printf("Snapshot - Expecting 1 snapshot, received %d snapshots. Only using snapshot %s", len(snapshotIds), snapshotId)
	}

	snapshot, err := ahashr.GetSnapshot(snapshotId)
	if err != nil {
		return err
	}

	volumeId := *snapshot.VolumeId
	if volumeId == "vol-ffffffff" {
		volumeId, err = ahashr.CreateVolume(snapshotId, volumeSize, ahashr.region)
		if err != nil {
			return err
		}
	}
	a.volumeId = volumeId

	if err := ahashr.waitForVolumeState(volumeId, types.VolumeStateAvailable, a.maxWaitDuration); err != nil {
		log.Printf("error waiting for the volume state of the volume %s", volumeId)
	}

	a.deviceName, err = ahashr.GetAvailableDeviceName()
	if err != nil {
		return fmt.Errorf("error getting available device name to attach volume %s: %v", a.volumeId, err)
	}

	if err := ahashr.AttachVolume(a.deviceName, ahashr.instanceId, a.volumeId); err != nil {
		return err
	}

	if err := ahashr.waitForAttachmentState(a.volumeId, ahashr.instanceId, types.VolumeAttachmentStateAttached, a.maxWaitDuration); err != nil {
		return err
	}

	log.Printf("DiskArchive - Starting creation of %s", a.archiveName)
	outputPath := filepath.Join(a.remotePath, a.archiveName)
	sshcmd := fmt.Sprintf("/usr/local/sbin/hashr-archive %s %s %s", a.deviceName, outputPath, a.bucketName)
	_, err = ahashr.RunSSHCommand(sshcmd)
	if err != nil {
		return err
	}

	outputDoneFile := fmt.Sprintf("%s.done", filepath.Join(a.remotePath, a.archiveName))
	log.Printf("DiskArchive - Waiting for the generation of archive %s in %s", a.archiveName, outputDoneFile)

	outputGenerated := false
	for i := 0; i < 2*a.maxWaitDuration; i++ {
		sshcmd := fmt.Sprintf("ls %s", outputDoneFile)
		out, err := ahashr.RunSSHCommand(sshcmd)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		if strings.Contains(out, outputDoneFile) {
			outputGenerated = true
			break
		}

		time.Sleep(1 * time.Second)
	}

	if !outputGenerated {
		return fmt.Errorf("archive %s is not generated within %d seconds", outputDoneFile, 2*a.maxWaitDuration)
	}

	log.Printf("DiskArchive - Generated archive %s from device %s", a.archiveName, a.deviceName)

	return nil
}

func (a *AwsImage) download() error {

	outputFile := filepath.Join(a.localPath, a.archiveName)

	log.Printf("ArchiveDownload - Starting download of %s from S3 bucket %s", a.archiveName, a.bucketName)
	if err := ahashr.DownloadImage(a.bucketName, a.archiveName, outputFile); err != nil {
		return err
	}
	log.Printf("ArchiveDownload - Completed the download of %s to %s", a.archiveName, outputFile)

	return nil // default
}

func (a *AwsImage) cleanup(deleteBucketArchive bool) error {
	// Delete done file
	doneFile := filepath.Join(a.remotePath, fmt.Sprintf("%s.done", a.archiveName))

	log.Printf("Cleanup - Deleting %s on remote system %s", doneFile, ahashr.instanceId)
	remoteCmd := fmt.Sprintf("rm -f %s", doneFile)
	_, err := ahashr.RunSSHCommand(remoteCmd)
	if err != nil {
		return err
	}

	// Detach volume from EC2 instance
	log.Printf("Cleanup - Detaching volume %s (%s) from instance %s", a.volumeId, a.deviceName, ahashr.instanceId)
	if err := ahashr.DetachVolume(a.deviceName, ahashr.instanceId, a.volumeId); err != nil {
		return err
	}

	if err := ahashr.waitForVolumeState(a.volumeId, types.VolumeStateAvailable, a.maxWaitDuration); err != nil {
		return err
	}

	// Deleting volume
	log.Printf("Cleanup - Deleting volume %s", a.volumeId)
	if err := ahashr.DeleteVolume(a.volumeId); err != nil {
		return err
	}

	for i := 0; i < a.maxWaitDuration; i++ {
		ok, err := ahashr.VolumeExists(a.volumeId)
		if err != nil {
			return err
		}
		if !ok {
			log.Printf("VolumeDeletion - Volume %s is deleted", a.volumeId)
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Deregister image
	log.Printf("Cleanup - Deleting image %s", a.imageId)
	if err := ahashr.DeregisterImage(a.imageId); err != nil {
		return err
	}

	for i := 0; i < a.maxWaitDuration; i++ {
		ok, err := ahashr.ImageExists(a.imageId)
		if err != nil {
			return err
		}

		if !ok {
			log.Printf("ImageDeletion - Image %s is deleted", a.imageId)
			break
		}

		time.Sleep(1 * time.Second)
	}

	// Delete archive from the bucket
	log.Printf("Cleanup - Deleting S3 bucket image %s", filepath.Join(a.bucketName, a.archiveName))
	if deleteBucketArchive {
		if err := ahashr.DeleteBucketImage(a.bucketName, a.archiveName); err != nil {
			return err
		}
	}

	return nil // default
}
