package spore

import (
	"fmt"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
)

type Instance struct {
	Attrs ecs.InstanceAttributesType
	Disk  ecs.DiskItemType
	next  *Instance
	prev  *Instance
	list  *InstanceList
}

func (instance *Instance) Start() error {
	if instance.Attrs.Status != "Stopped" {
		return fmt.Errorf("Not stopped instance not support start action")
	}
	client := AliyunClient()

	// remove used tag
	tagArgs := ecs.RemoveTagsArgs{
		ResourceId:   instance.Attrs.InstanceId,
		ResourceType: "instance",
		RegionId:     common.Region(AliyunRegionId),
		Tag:          UsedInstanceTag,
	}
	client.RemoveTags(&tagArgs)

	// reinit disk
	err := client.ReInitDisk(instance.Disk.DiskId)
	if err != nil {
		log.Errorf("init instnace %s disk %s error %s", instance.Attrs.InstanceId, instance.Disk.DiskId, err)
		return err
	}

	// sleep few seconds, if not start instance will be failed because reinit disk task dot not finished
	time.Sleep(2 * time.Second)

	// start instance
	return client.StartInstance(instance.Attrs.InstanceId)
}

// remove instance tag
func (instance *Instance) Stop() error {
	if instance.Attrs.Status != "Running" {
		return fmt.Errorf("Not running instance not support stop action")
	}

	client := AliyunClient()

	// remove tag
	tagArgs := ecs.RemoveTagsArgs{
		ResourceId:   instance.Attrs.InstanceId,
		ResourceType: "instance",
		RegionId:     common.Region(AliyunRegionId),
		Tag:          UsedInstanceTag,
	}
	client.RemoveTags(&tagArgs)

	// stop
	return client.StopInstance(instance.Attrs.InstanceId, true)
}

// set instance tag
func (instance *Instance) SetUsedTag() error {
	// remove used tag
	tagArgs := ecs.AddTagsArgs{
		ResourceId:   instance.Attrs.InstanceId,
		ResourceType: "instance",
		RegionId:     common.Region(AliyunRegionId),
		Tag:          UsedInstanceTag,
	}

	client := AliyunClient()
	return client.AddTags(&tagArgs)
}

type InstanceList struct {
	sync.Mutex
	root *Instance
	size int
}

func (l *InstanceList) Init() *InstanceList {
	l.Lock()
	defer l.Unlock()

	l.root = new(Instance)
	l.root.next = l.root
	l.root.prev = l.root
	l.size = 0
	return l
}

func NewInstanceList() *InstanceList {

	return new(InstanceList).Init()
}

func (l *InstanceList) Len() int {
	return l.size
}

func (l *InstanceList) lazyInit() {
	l.Lock()
	defer l.Unlock()
	if l.root.next == nil {
		l.Init()
	}
}

func (l *InstanceList) insert(e, at *Instance) *Instance {
	l.Lock()
	defer l.Unlock()
	n := at.next
	at.next = e
	e.prev = at
	e.next = n
	n.prev = e
	e.list = l
	l.size++
	return e
}

func (l *InstanceList) Push(e *Instance) *Instance {
	l.lazyInit()
	// check if is exist
	for n := l.root.next; n != l.root; n = n.next {
		if n.Attrs.InstanceId == e.Attrs.InstanceId {
			return nil
		}
	}
	return l.insert(e, l.root.prev)
}

func (l *InstanceList) Remove(e *Instance) *Instance {
	l.Lock()
	defer l.Unlock()
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil
	e.prev = nil
	e.list = nil
	l.size--
	return e
}

func (l *InstanceList) Find(imageId string) *Instance {
	for n := l.root.next; n != l.root; n = n.next {
		if n.Attrs.ImageId == imageId {
			return l.Remove(n)
		}
	}
	return nil
}
