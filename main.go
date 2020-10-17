package main

import (
	"fmt"
	mtp "github.com/ganeshrvel/go-mtpfs/mtp"
	"github.com/kr/pretty"
	"log"
	"strings"
)

// initialize the mtp device
// returns mtp device
func Initialize(init Init) (*mtp.Device, error) {
	dev, err := mtp.SelectDevice("")

	if err != nil {
		return nil, MtpDetectFailedError{error: err}
	}

	dev.MTPDebug = init.debugMode
	dev.DataDebug = init.debugMode
	dev.USBDebug = init.debugMode

	dev.Timeout = devTimeout

	if err = dev.Configure(); err != nil {
		return nil, ConfigureError{error: err}
	}

	return dev, nil
}

// close the mtp device
func Dispose(dev *mtp.Device) {
	defer dev.Close()
}

// fetch device info
func FetchDeviceInfo(dev *mtp.Device) (*mtp.DeviceInfo, error) {
	info := mtp.DeviceInfo{}
	err := dev.GetDeviceInfo(&info)

	if err != nil {
		return nil, DeviceInfoError{error: err}
	}

	return &info, nil
}

// fetch storages
func FetchStorages(dev *mtp.Device) ([]StorageData, error) {
	sids := mtp.Uint32Array{}
	if err := dev.GetStorageIDs(&sids); err != nil {
		return nil, StorageInfoError{error: err}
	}

	if len(sids.Values) < 1 {
		return nil, NoStorageError{error: fmt.Errorf("no storage found")}
	}

	var result []StorageData

	for sid := range sids.Values {
		var info mtp.StorageInfo
		if err := dev.GetStorageInfo(uint32(sid), &info); err != nil {
			return nil, StorageInfoError{error: err}
		}

		result = append(result, StorageData{
			sid:  sids.Values[0],
			info: info,
		})
	}

	return result, nil
}

// fetch file info using object id
func FetchFile(dev *mtp.Device, objectId uint32, parentPath string) (*FileInfo, error) {
	obj := mtp.ObjectInfo{}
	if err := dev.GetObjectInfo(objectId, &obj); err != nil {
		return nil, FileObjectError{error: err}
	}

	size, _ := GetFileSize(dev, &obj, objectId)
	isDir := isObjectADir(&obj)
	filename := obj.Filename
	_parentPath := fixDirSlash(parentPath)
	fullPath := getFullPath(_parentPath, filename)

	return &FileInfo{
		Info:       &obj,
		Size:       size,
		IsDir:      isDir,
		ModTime:    obj.ModificationDate,
		Name:       obj.Filename,
		FullPath:   fullPath,
		ParentPath: _parentPath,
		Extension:  extension(obj.Filename, isDir),
		ParentId:   obj.ParentObject,
		ObjectId:   objectId,
	}, nil
}

// list the contents in a directory
// [objectId] and [parentPath] are optional parameters
// if [objectId] is not available then parentPath is used to fetch objectId
// dont leave both [objectId] and [parentPath] empty
func ListDirectory(dev *mtp.Device, storageId, objectId uint32, parentPath string) (*[]FileInfo, error) {
	_objectId := objectId

	// if objectId is not available then fetch the objectId from parentPath
	if _objectId == 0 {
		objId, err := GetObjectIdFromPath(dev, storageId, parentPath)

		if err != nil {
			return nil, err
		}

		_objectId = objId
	}

	handles := mtp.Uint32Array{}
	if err := dev.GetObjectHandles(storageId, mtp.GOH_ALL_ASSOCS, _objectId, &handles); err != nil {
		return nil, ListDirectoryError{error: err}
	}

	var fileInfoList []FileInfo

	for _, objectId := range handles.Values {
		fi, err := FetchFile(dev, objectId, parentPath)

		if err != nil {
			continue
		}

		fileInfoList = append(fileInfoList, *fi)
	}

	return &fileInfoList, nil
}

func GetFileSize(dev *mtp.Device, obj *mtp.ObjectInfo, objectId uint32) (int64, error) {
	var size int64
	if obj.CompressedSize == 0xffffffff {
		var val mtp.Uint64Value
		if err := dev.GetObjectPropValue(objectId, mtp.OPC_ObjectSize, &val); err != nil {
			return 0, FileObjectError{
				fmt.Errorf("GetObjectPropValue handle %d failed: %v", objectId, err),
			}
		}

		size = int64(val.Value)
	} else {
		size = int64(obj.CompressedSize)
	}

	return size, nil
}

func GetObjectIdFromFilename(dev *mtp.Device, storageId uint32, parentId uint32, filename string) (objectID uint32, isDir bool, error error) {
	handles := mtp.Uint32Array{}
	if err := dev.GetObjectHandles(storageId, mtp.GOH_ALL_ASSOCS, parentId, &handles); err != nil {
		return 0, false, FileObjectError{error: err}
	}

	for _, objectId := range handles.Values {
		obj := mtp.ObjectInfo{}
		if err := dev.GetObjectInfo(objectId, &obj); err != nil {
			return 0, false, FileObjectError{error: err}
		}

		// return the current objectId if the filename == obj.Filename
		if obj.Filename == filename {
			return objectId, isObjectADir(&obj), nil
		}
	}

	return 0, false, FileNotFoundError{error: fmt.Errorf("file not found: %s", filename)}
}

func GetObjectIdFromPath(dev *mtp.Device, storageId uint32, filePath string) (uint32, error) {
	_filePath := fixDirSlash(filePath)

	splittedFilePath := strings.Split(_filePath, "/")

	if _filePath == "/" {
		return mtp.GOH_ROOT_PARENT, nil
	}

	var result = uint32(mtp.GOH_ROOT_PARENT)
	var resultCount = 0
	const skipIndex = 1

	for i, fName := range splittedFilePath[skipIndex:] {
		objectId, isDir, err := GetObjectIdFromFilename(dev, storageId, result, fName)

		if err != nil {
			switch err.(type) {
			case FileNotFoundError:
				return 0, InvalidPathError{
					error: fmt.Errorf("path not found: %s\nreason: %v", filePath, err),
				}

			default:
				return 0, err
			}
		}

		if !isDir && indexExists(splittedFilePath, i+1+skipIndex) {
			return 0, InvalidPathError{error: fmt.Errorf("path not found: %s", filePath)}
		}

		result = objectId
		resultCount += 1
	}

	if resultCount < 1 {
		return 0, InvalidPathError{error: fmt.Errorf("file not found: %s", filePath)}
	}

	return result, nil
}

func FileExists(dev *mtp.Device, storageId uint32, filePath string) bool {
	if _, err := GetObjectIdFromPath(dev, storageId, filePath); err != nil {
		return false
	}

	return true
}

func main() {
	dev, err := Initialize(Init{})

	if err != nil {
		log.Panic(err)
	}

	_, err = FetchDeviceInfo(dev)
	if err != nil {
		log.Panic(err)
	}

	storages, err := FetchStorages(dev)
	if err != nil {
		log.Panic(err)
	}

	sid := storages[0].sid

	files, err := ListDirectory(dev, sid, 0, "/test/")
	if err != nil {
		log.Panic(err)
	}

	pretty.Println(files)

	/*fileObj, err := GetObjectIdFromPath(dev, sid, "/tests/s")
	if err != nil {
		log.Panic(err)
	}

	pretty.Println("======\n")

	pretty.Println(fileObj)
	*/

	/*exists := FileExists(dev, sid, "/tests/test.txt")

	pretty.Println("======\n")

	pretty.Println("Does File exists:", exists)*/
	Dispose(dev)
}
