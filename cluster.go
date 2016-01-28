package spore

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/denverdino/aliyungo/common"
	"github.com/denverdino/aliyungo/ecs"
)

// delayer offers a simple API to random delay within a given time range.
type delayer struct {
	rangeMin time.Duration
	rangeMax time.Duration

	r *rand.Rand
	l sync.Mutex
}

func newDelayer(rangeMin, rangeMax time.Duration) *delayer {
	return &delayer{
		rangeMin: rangeMin,
		rangeMax: rangeMax,
		r:        rand.New(rand.NewSource(time.Now().UTC().UnixNano())),
	}
}

func (d *delayer) Wait() <-chan time.Time {
	d.l.Lock()
	defer d.l.Unlock()

	waitPeriod := int64(d.rangeMin)
	if delta := int64(d.rangeMax) - int64(d.rangeMin); delta > 0 {
		waitPeriod += d.r.Int63n(delta)
	}
	return time.After(time.Duration(waitPeriod))
}

type Cluster struct {
	stopCh         chan struct{}
	refreshDelayer *delayer

	starting   *InstanceList
	avavilable *InstanceList
	stopping   *InstanceList
	used       map[string]*Instance

	disks     []ecs.DiskItemType
	instances map[string]*Instance
}

func NewCluster() (*Cluster, error) {
	cluster := &Cluster{
		refreshDelayer: newDelayer(RefreshMinInterval, RefreshMaxInterval),
		starting:       NewInstanceList(),
		avavilable:     NewInstanceList(),
		stopping:       NewInstanceList(),
		instances:      make(map[string]*Instance),
		used:           make(map[string]*Instance),
	}
	if err := cluster.RefreshDisks(); err != nil {
		return nil, err
	}
	if err := cluster.RefreshInstances(); err != nil {
		return nil, err
	}
	go cluster.refreshLoop()
	return cluster, nil
}

// find instance based on instance id
func (c *Cluster) Find(instanceId string) *Instance {
	instance, ok := c.instances[instanceId]
	if ok {
		return instance
	}
	return nil
}

// get a instance from running instance based on image
func (c *Cluster) GetInstance(imageId string) *Instance {
	instance := c.avavilable.Find(imageId)
	if instance == nil {
		return nil
	}
	if err := instance.SetUsedTag(); err != nil {
		log.Errorf("set instance %s used tag error %s", instance.Attrs.InstanceId, err)
		c.avavilable.Push(instance)
		return nil
	}
	c.used[instance.Attrs.InstanceId] = instance
	log.Debugf("instance %s has been used", instance.Attrs.InstanceId)
	return instance
}

// stop a instance with running status
func (c *Cluster) StopInstance(instanceId string) error {
	instance, ok := c.instances[instanceId]
	if !ok {
		return fmt.Errorf("not found instance")
	}
	instance, ok = c.used[instanceId]
	if !ok {
		return fmt.Errorf("not used instance can not been stop")
	}
	if err := instance.Stop(); err != nil {
		log.Errorf("stop instance %s failed: %s", instance.Attrs.InstanceId, err)
		return err
	}
	delete(c.used, instance.Attrs.InstanceId)
	log.Debugf("stop instance %s", instance.Attrs.InstanceId)

	// try to start instance, because force stop instance will be finished quickly
	go func() {
		time.Sleep(3 * time.Second)
		status := instance.Attrs.Status
		instance.Attrs.Status = "Stopped"
		if err := instance.Start(); err != nil {
			instance.Attrs.Status = status
		}
	}()

	return nil
}

// refresh disk and instance status
func (c *Cluster) refreshLoop() {
	for {
		select {
		case <-c.refreshDelayer.Wait():
		case <-c.stopCh:
			return
		}
		c.RefreshDisks()
		c.RefreshInstances()
		log.Debugf("finished refresh instances,  total %d, available %d, used %d, sopping %d, starting %d", len(c.instances), c.avavilable.Len(), len(c.used), c.stopping.Len(), c.starting.Len())
	}
}

//  get all disks
func (c *Cluster) RefreshDisks() error {
	client := AliyunClient()
	diskArgs := ecs.DescribeDisksArgs{
		RegionId: common.Region(AliyunRegionId),
		ZoneId:   AliyunZoneId,
	}
	diskArgs.Pagination = NewPagination()
	disks, pagination, err := client.DescribeDisks(&diskArgs)
	if err != nil {
		return err
	}
	c.disks = disks
	for {
		nextPage := pagination.NextPage()
		if nextPage != nil {
			diskArgs.Pagination = *nextPage
			disks, pagination, err = client.DescribeDisks(&diskArgs)
			if err != nil {
				return err
			}
			c.disks = append(c.disks, disks...)
		} else {
			break
		}
	}
	log.Debugf("Total found %d disk", len(c.disks))
	return nil
}

// get all instances, instance is processed differently based on status, instance have four
// status: Starting, Running, Stoping, Stopped, refer:
// https://help.aliyun.com/document_detail/ecs/open-api/appendix/instancestatustable.html
// Running: instance with status running may being used or may be available
// Sopped: instance with status soppend shoult to be started, into starting status
func (c *Cluster) RefreshInstances() error {
	var instances, pageInstances []ecs.InstanceAttributesType
	client := AliyunClient()
	instanceArgs := ecs.DescribeInstancesArgs{
		RegionId:        common.Region(AliyunRegionId),
		ZoneId:          AliyunZoneId,
		SecurityGroupId: AliyunSecurityGroupId,
	}
	instanceArgs.Pagination = NewPagination()
	instances, pagination, err := client.DescribeInstances(&instanceArgs)
	if err != nil {
		return err
	}
	for {
		nextPage := pagination.NextPage()
		if nextPage != nil {
			instanceArgs.Pagination = *nextPage
			pageInstances, pagination, err = client.DescribeInstances(&instanceArgs)
			if err != nil {
				return err
			}
			instances = append(instances, pageInstances...)
		} else {
			break
		}
	}
	log.Debugf("Total found %d instances", len(instances))

	// query all used instance based on tag
	tagArgs := ecs.DescribeResourceByTagsArgs{
		ResourceType: "instance",
		RegionId:     common.Region(AliyunRegionId),
		Tag:          UsedInstanceTag,
	}
	tagArgs.Pagination = NewPagination()
	var resources, pageResources []ecs.ResourceItemType
	resources, pagination, err = client.DescribeResourceByTags(&tagArgs)
	if err != nil {
		return err
	}
	for {
		nextPage := pagination.NextPage()
		if nextPage != nil {
			tagArgs.Pagination = *nextPage
			pageResources, pagination, err = client.DescribeResourceByTags(&tagArgs)
			if err != nil {
				return err
			}
			resources = append(resources, pageResources...)
		} else {
			break
		}
	}
	// all used instance
	for _, resource := range resources {
		instance, ok := c.instances[resource.ResourceId]
		if !ok {
			instance = new(Instance)
			c.instances[resource.ResourceId] = instance
			c.used[resource.ResourceId] = instance
		} else {
			// used instance should not in any list
			if instance.list != nil {
				instance.list.Remove(instance)
			}
			c.used[resource.ResourceId] = instance
		}
	}

	log.Debugf("Total found %d used instances", len(c.used))

	// check instance status
	for _, node := range instances {
		instance, ok := c.instances[node.InstanceId]
		if !ok {
			instance = new(Instance)
		}

		// find instance disk id
		for _, disk := range c.disks {
			if disk.InstanceId == node.InstanceId {
				instance.Disk = disk
				break
			}
		}
		if len(instance.Disk.DiskId) == 0 {
			continue
		}
		instance.Attrs = node
		c.instances[node.InstanceId] = instance
		switch instance.Attrs.Status {
		case "Running":
			if _, ok := c.used[instance.Attrs.InstanceId]; !ok {
				// if instance in other list except running list, should remove it from the list
				if instance.list == nil {
					c.avavilable.Push(instance)
				} else if instance.list != c.avavilable {
					instance.list.Remove(instance)
					c.avavilable.Push(instance)
				}
			} else {
				c.used[instance.Attrs.InstanceId] = instance
			}
		case "Starting":
			if instance.list == nil {
				c.starting.Push(instance)
			} else if instance.list != c.starting {
				instance.list.Remove(instance)
				c.starting.Push(instance)
			}
		case "Stopping":
			if instance.list == nil {
				c.stopping.Push(instance)

			} else if instance.list != c.stopping {
				instance.list.Remove(instance)
				c.stopping.Push(instance)
			}
			// reinit disk, then start instance
			// FIXME if more than N instances to start, will sleep N*2 second
		case "Stopped":
			if instance.list != nil {
				instance.list.Remove(instance)
			}
			if err := instance.Start(); err != nil {
				log.Errorf("start instance %s failed, status %s,  %s", instance.Attrs.InstanceId, instance.Attrs.Status, err)
			} else {
				log.Debugf("instance %s has be started", instance.Attrs.InstanceId)
			}
		}
	}
	return nil
}
