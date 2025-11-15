package engine

import (
	"container/heap"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"argo/pkg/conf"
	"argo/pkg/log"
)

type Scheduler struct {
	engine   *EngineInfo
	submitCh chan *UrlInfo
	tabQueue chan *UrlInfo
	tabLimit chan struct{}
	tabWg    sync.WaitGroup

	maxQueueSize int
	pq           priorityQueue
	pqMutex      sync.Mutex
	pqCond       *sync.Cond
	seq          int64

	rateLimiter <-chan time.Time
	ticker      *time.Ticker
}

func NewScheduler(engine *EngineInfo) *Scheduler {
	maxTabs := conf.GlobalConfig.BrowserConf.TabCount
	if maxTabs <= 0 {
		maxTabs = 1
	}
	queueSize := conf.GlobalConfig.BrowserConf.QueueSize
	if queueSize <= 0 {
		queueSize = 100000
	}
	scheduleInterval := conf.GlobalConfig.BrowserConf.ScheduleInterval
	s := &Scheduler{
		engine:       engine,
		submitCh:     make(chan *UrlInfo, queueSize),
		tabQueue:     make(chan *UrlInfo, maxTabs),
		tabLimit:     make(chan struct{}, maxTabs),
		maxQueueSize: queueSize,
		pq:           make(priorityQueue, 0, 128),
	}
	s.pqCond = sync.NewCond(&s.pqMutex)
	if scheduleInterval > 0 {
		ticker := time.NewTicker(time.Duration(scheduleInterval) * time.Millisecond)
		s.ticker = ticker
		s.rateLimiter = ticker.C
	}
	return s
}

func (s *Scheduler) Start() {
	go s.ingestLoop()
	go s.dispatchLoop()
	go s.tabWork()
}

func (s *Scheduler) Submit(uif *UrlInfo) {
	if uif == nil {
		return
	}
	s.submitCh <- uif
}

func (s *Scheduler) WaitQueueEmpty() {
	for {
		s.pqMutex.Lock()
		empty := len(s.submitCh) == 0 && s.pq.Len() == 0
		s.pqMutex.Unlock()
		if empty {
			return
		}
		time.Sleep(time.Second)
	}
}

func (s *Scheduler) WaitTabs() {
	s.tabWg.Wait()
}

func (s *Scheduler) ingestLoop() {
	for uif := range s.submitCh {
		if uif == nil || uif.Url == "" {
			continue
		}
		if strings.Contains(uif.Url, "http") && !strings.Contains(uif.Url, s.engine.Host) {
			continue
		}
		if filterStatic(uif.Url) {
			continue
		}
		if !s.engine.urlIsExists(uif.Url) {
			if !s.enqueue(uif) {
				log.Logger.Warnf("queue full drop: %s", uif.Url)
			}
		}
	}
}

func (s *Scheduler) enqueue(uif *UrlInfo) bool {
	s.pqMutex.Lock()
	defer s.pqMutex.Unlock()
	if s.pq.Len() >= s.maxQueueSize {
		atomic.AddInt64(&s.engine.UrlsDropped, 1)
		s.engine.EmitEvent(EngineEvent{Type: "url_dropped", Target: uif.Url, Timestamp: time.Now()})
		return false
	}
	seq := atomic.AddInt64(&s.seq, 1)
	heap.Push(&s.pq, &priorityItem{info: uif, depth: uif.Depth, seq: seq})
	s.pqCond.Signal()
	return true
}

func (s *Scheduler) dispatchLoop() {
	for {
		s.pqMutex.Lock()
		for s.pq.Len() == 0 {
			s.pqCond.Wait()
		}
		item := heap.Pop(&s.pq).(*priorityItem)
		s.pqMutex.Unlock()
		if s.rateLimiter != nil {
			<-s.rateLimiter
		}
		s.pushTabQueue(item.info)
	}
}

func (s *Scheduler) pushTabQueue(uif *UrlInfo) {
	log.Logger.Debugf("submit url: %s sourceType: %s sourceUrl: %s", uif.Url, uif.SourceType, uif.SourceUrl)
	s.tabQueue <- uif
}

func (s *Scheduler) tabWork() {
	for {
		select {
	case s.tabLimit <- struct{}{}:
		uif := <-s.tabQueue
			if uif == nil {
				<-s.tabLimit
				continue
			}
			if uif.Depth > conf.GlobalConfig.BrowserConf.MaxDepth {
				log.Logger.Debugf("[ Max Depth] => %s depth: %d", uif.Url, uif.Depth)
				<-s.tabLimit
				continue
			}
			if !strings.Contains(uif.Url, s.engine.Host) {
				<-s.tabLimit
				continue
			}
			s.tabWg.Add(1)
			go func(info *UrlInfo) {
				defer func() {
					<-s.tabLimit
					s.tabWg.Done()
					s.engine.EmitEvent(EngineEvent{Type: "tab_finish", Target: info.Url, Timestamp: time.Now()})
				}()
				s.engine.EmitEvent(EngineEvent{Type: "tab_start", Target: info.Url, Timestamp: time.Now(), Data: map[string]interface{}{"depth": info.Depth}})
				if info.SourceType == "homePage" {
					s.engine.NewTab(info, HOME_PAGE_FLAG)
				} else {
					s.engine.NewTab(info, NOT_HOME_PAGE_FLAG)
				}
			}(uif)
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
}
