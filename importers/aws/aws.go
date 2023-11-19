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

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/google/hashr/core/hashr"
)

const (
	// repoName contains the repository name.
	repoName = "AWS"
)

var ahashr *awsHashR

type AwsImage struct {
	imageId         string       // AMI in HashR project
	image           *types.Image // Image in HashR project
	sourceImageId   string       // AMI owned by AWS
	sourceImage     *types.Image // Source Image owned by Amazon
	localPath       string
	remotePath      string
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
	return repoName
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

		// If we have source image ID, we can get the image details.
		if ahashr == nil {
			return "", fmt.Errorf("awsHashR object not initialized")
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
	osName     string      // Repo filtered by OS name
	osArchs    []string    // Repo filtered by OS architectures
	instanceId string      // EC2 instance
	images     []*AwsImage // Source images owned by Amazon
}

// NewRepo returns a new AWS repo.
func NewRepo(ctx context.Context, instanceId string, osName string, osArchs []string) (*Repo, error) {
	// Setup awsHashR object ahashr
	ahashr = NewAwsHashR()
	ahashr.SetupClient(instanceId)

	return &Repo{
		osName:     osName,
		osArchs:    osArchs,
		instanceId: instanceId,
	}, nil
}

// RepoName returns the AWS repository name.
func (r *Repo) RepoName() string {
	return repoName
}

// DiscoverRepo traverses the repository and looks for the AMIs.
func (r *Repo) DiscoverRepo() ([]hashr.Source, error) {
	var sources []hashr.Source

	images, err := ahashr.GetAmazonImages(r.osName)
	if err != nil {
		return nil, err
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("nil or no images matching OS %s", r.osName)
	}

	for _, image := range images {
		awsimage := &AwsImage{
			sourceImageId: *image.ImageId,
			sourceImage:   &image,
		}

		r.images = append(r.images, awsimage)
		sources = append(sources, awsimage)
	}

	return sources, nil
}

// Preprocess extracts the content of the image.
func (a *AwsImage) Preprocess() (string, error) {

	return "", nil // default return
}
