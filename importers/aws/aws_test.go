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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPreprocess(t *testing.T) {
	ahashr = newTestAwsHashR()
	awsimage := NewAwsImage()

	config := getTestingConfig("downloadimage")
	//fmt.Println(config)
	//fmt.Println(ahashr)

	awsimage.sourceImageId = config["sourceimageid"].(string)
	awsimage.archiveName = fmt.Sprintf("%s-raw.dd.gz", awsimage.sourceImageId)
	awsimage.bucketName = config["bucketname"].(string)
	awsimage.localPath = config["localpath"].(string)
	awsimage.remotePath = config["remotepath"].(string)
	awsimage.maxWaitDuration = config["maxWaitDuration"].(int)

	sourceimage, err := ahashr.GetImageDetail(awsimage.sourceImageId)
	assert.Nil(t, err)
	awsimage.sourceImage = sourceimage

	_, err = awsimage.Preprocess()
	assert.Nil(t, err)
}
