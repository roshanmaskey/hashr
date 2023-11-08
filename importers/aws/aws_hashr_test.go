package aws

import (
	"io/ioutil"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

var ahashr *awsHashR
var configdata map[string]interface{}

func loadTestingConfig() {
	configfile := filepath.Join("..", "..", "data", "test_config.yaml")
	data, err := ioutil.ReadFile(configfile)
	if err != nil {
		log.Fatalf("failed reading config file: %v", err)
	}

	if err := yaml.Unmarshal(data, &configdata); err != nil {
		log.Fatalf("error parsing config: %v", err)
	}
}

func init() {
	loadTestingConfig()

	ahashr = NewAwsHashR()

	if err := ahashr.SetupClient(); err != nil {
		log.Fatal(err)
	}
}

func getTestingConfig(configname string) map[interface{}]interface{} {
	config := configdata[configname]

	if config == nil {
		log.Fatalf("error getting config for %s: %v", configname, err)
	}

	return config.(map[interface{}]interface{})
}

func TestCopyAndDeregisterImage(t *testing.T) {

	config := getTestingConfig("copyandderegisterimage")
	sourceimageid := config["sourceimageid"].(string)
	sourceregion := config["sourceregion"].(string)
	targetimagename := config["targetimagename"].(string)

	imageid, err := ahashr.CopyImage(sourceimageid, sourceregion, targetimagename)
	assert.Nil(t, err)
	assert.NotEqual(t, "", imageid)

	time.Sleep(1 * time.Second)

	err = ahashr.DeregisterImage(imageid)
	assert.Nil(t, err)
}

func TestVolumeState(t *testing.T) {
	config := getTestingConfig("volumestate")
	volumeid := config["volumeid"].(string)

	state, err := ahashr.GetVolumeState(volumeid)
	assert.Equal(t, types.VolumeStateInUse, state)
	assert.Nil(t, err)
}

func TestSnapshotState(t *testing.T) {
	config := getTestingConfig("snapshotstate")
	snapshotid := config["snapshotid"].(string)

	state, err := ahashr.GetSnapshotState(snapshotid)
	assert.Equal(t, types.SnapshotStateCompleted, state)
	assert.Nil(t, err)
}

func TestGetImageDetail(t *testing.T) {
	config := getTestingConfig("getimagedetail")
	sourceimageid := config["sourceimageid"].(string)
	targetimagename := config["targetimagename"].(string)

	image, err := ahashr.GetImageDetail(sourceimageid)
	assert.Equal(t, sourceimageid, *image.ImageId)

	imagename := strings.Split(*image.ImageLocation, "/")[1]
	assert.Equal(t, targetimagename, imagename)
	assert.Nil(t, err)
}

func TestCreateVolume(t *testing.T) {
	config := getTestingConfig("createvolume")
	snapshotid := config["snapshotid"].(string)
	disksize := int32(config["disksize"].(int))

	instance := getTestingConfig("instance")
	region := instance["region"].(string)

	volumeid, err := ahashr.CreateVolume(snapshotid, disksize, region)
	assert.Nil(t, err)
	assert.NotEqual(t, "", volumeid)

	err = ahashr.DeleteVolume(volumeid)
	assert.Nil(t, err)
}

func TestAttachVolume(t *testing.T) {
	config := getTestingConfig("attachvolume")
	snapshotid := config["snapshotid"].(string)
	disksize := int32(config["disksize"].(int))
	deviceid := config["device"].(string)

	instance := getTestingConfig("instance")
	instanceid := instance["instanceid"].(string)
	region := instance["region"].(string)

	volumeid, err := ahashr.CreateVolume(snapshotid, int32(disksize), region)
	assert.Nil(t, err)
	assert.NotEqual(t, "", volumeid)

	err = ahashr.waitForVolumeState(volumeid, types.VolumeStateAvailable, 600)
	assert.Nil(t, err)

	err = ahashr.AttachVolume(deviceid, instanceid, volumeid)
	if err != nil {
		log.Fatal(err)
	}
	assert.Nil(t, err)

	log.Println("Sleeping for 5 seconds after attachment")
	time.Sleep(5 * time.Second)

	// Detach volume
	err = ahashr.DetachVolume(deviceid, instanceid, volumeid)
	assert.Nil(t, err)

	err = ahashr.waitForVolumeState(volumeid, types.VolumeStateAvailable, 600)
	assert.Nil(t, err)

	err = ahashr.DeleteVolume(volumeid)
	assert.Nil(t, err)
}

func TestSSHClientSetup(t *testing.T) {
	instance := getTestingConfig("instance")

	keyname := instance["keyname"].(string)
	server := instance["publicdnsname"].(string)
	user := instance["user"].(string)

	err := ahashr.SSHClientSetup(user, keyname, server)
	assert.Nil(t, err)
	assert.NotNil(t, ahashr.sshclient)
}

func TestRunSSHCommand(t *testing.T) {
	instance := getTestingConfig("instance")

	keyname := instance["keyname"].(string)
	server := instance["publicdnsname"].(string)
	user := instance["user"].(string)

	err := ahashr.SSHClientSetup(user, keyname, server)
	assert.Nil(t, err)

	err = ahashr.RunSSHCommand("ls -lh ~/")
	assert.Nil(t, err)
}
