package orca

import (
	"strings"
	"sync"

	"github.com/Andrew-Morozko/orca/jobcontroller"
	"github.com/Andrew-Morozko/orca/orca/errctrl"
	"github.com/Andrew-Morozko/orca/orca/mydocker"

	"github.com/pkg/errors"
)

type ImageKind = string

const (
	ImageKindWeb ImageKind = "web"
	ImageKindSSH ImageKind = "ssh"
	ImageKindTCP ImageKind = "tcp"
)

var ImageKinds = []ImageKind{ImageKindWeb, ImageKindSSH, ImageKindTCP}

var Docker *mydocker.Client

type ImageList struct {
	lock                sync.Mutex
	imagesByKindAndName map[ImageKind]map[string]*Image
	imagesByDockerID    map[string]*Image
	// TODO rn just storing the images in the map. Remove them
	// when all the clients have gone
	removedImages map[string]*Image
}

func NewImageList(jc jobcontroller.JobController) (il *ImageList, err error) {
	defer errctrl.Annotate(&err, "import images")
	jc = jc.AddLoggerPrefix("NewImageList")
	jc.Logger.Log("Loading images...")
	il = &ImageList{
		imagesByKindAndName: make(map[ImageKind]map[string]*Image),
		imagesByDockerID:    make(map[string]*Image),
		removedImages:       make(map[string]*Image),
	}
	for _, kind := range ImageKinds {
		il.imagesByKindAndName[kind] = make(map[string]*Image)
	}

	err = il.UpdateImages(jc)
	if err != nil {
		return
	}
	return il, nil
}

func (il *ImageList) UpdateImages(jc jobcontroller.JobController) (err error) {
	newImages, err := Docker.ListLabeledImages(jc)
	if err != nil {
		return err
	}

	newImgsById := make(map[string]*mydocker.Image)
	for _, img := range newImages {
		newImgsById[img.ID] = img
	}

	jc.Logger.Log("Performing image update")

	il.lock.Lock()
	defer il.lock.Unlock()

	var imgsToAdd []*mydocker.Image
	var imgsToRemove []*Image

	for dockerId, img := range il.imagesByDockerID {
		_, foundInNew := newImgsById[dockerId]
		if !foundInNew {
			imgsToRemove = append(imgsToRemove, img)
		}
	}
	for dockerId, img := range newImgsById {
		_, foundInOld := il.imagesByDockerID[dockerId]
		if !foundInOld {
			imgsToAdd = append(imgsToAdd, img)
		}
	}

	jc.Logger.Logf("%d images to add, %d images to remove", len(imgsToAdd), len(imgsToRemove))

	for _, img := range imgsToAdd {
		imgToAdd, err := NewImage(jc, img)
		if err != nil {
			jc.Logger.Errf(err,
				"Failed to parse image %s: %s",
				strings.Split(img.ID, ":")[1][:8],
			)
			continue
		}
		il.imagesByDockerID[imgToAdd.DockerID] = imgToAdd
		il.imagesByKindAndName[imgToAdd.Kind][imgToAdd.Name] = imgToAdd
	}

	for _, img := range imgsToRemove {
		delete(il.imagesByDockerID, img.DockerID)
		delete(il.imagesByKindAndName[img.Kind], img.Name)
		il.removedImages[img.DockerID] = img
		img.MarkRemoved()
	}

	return nil
}

func (il *ImageList) GetImages(kind ImageKind, ui *User) map[string]*Image {
	var mapCopy map[string]*Image
	il.lock.Lock()
	theMap := il.imagesByKindAndName[kind]
	mapCopy = make(map[string]*Image, len(theMap))
	for k, v := range theMap {
		mapCopy[k] = v
	}
	il.lock.Unlock()
	for k, v := range mapCopy {
		if !v.IsVisibleTo(ui) {
			delete(mapCopy, k)
		}
	}
	return mapCopy
}

var ImageNotFoundErr = errors.New("image not found")
var ImageNotAvailibleErr = errors.New("image not availible")

func (il *ImageList) GetImage(kind ImageKind, name string, ui *User) (image *Image, err error) {
	var found bool
	il.lock.Lock()
	image, found = il.imagesByKindAndName[kind][name]
	il.lock.Unlock()
	if !found {
		return nil, ImageNotFoundErr
	}
	if !image.IsVisibleTo(ui) {
		return nil, ImageNotAvailibleErr
	}
	return
}
