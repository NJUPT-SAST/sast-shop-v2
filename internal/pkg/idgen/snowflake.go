package idgen

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

const (
	workerIDBits = 10 // 工作节点ID，占10位，支持1024个节点
	sequenceBits = 12 // 序列号，占12位，每毫秒支持4096个ID

	MaxWorkerID = 1<<workerIDBits - 1 // 最大节点ID = 1023

	workerIDShift  = sequenceBits
	timestampShift = workerIDBits + sequenceBits
	maxSequence    = 1<<sequenceBits - 1 // 允许的最大时钟回拨

	defaultEpochMillis = int64(1704067200000)
	maxClockBackward   = 5 * time.Second
)

var (
	defaultSnowflakeOnce sync.Once
	defaultSnowflake     *Snowflake
	defaultSnowflakeErr  error
)

type Snowflake struct {
	mu          sync.Mutex // 互斥锁，保证并发安全
	epochMillis int64      // 起始时间戳
	workerID    int64      // 工作节点ID
	sequence    int64      // 当前毫秒内的序列号
	lastMillis  int64      // 上次生成ID的时间戳
}

type OrderNoGenerator struct {
	prefix    string
	snowflake *Snowflake
}

func NewSnowflake(workerID int64) (*Snowflake, error) {
	if err := validateWorkerID(workerID, "worker id"); err != nil {
		return nil, err
	}

	return &Snowflake{
		epochMillis: defaultEpochMillis,
		workerID:    workerID,
		lastMillis:  -1,
	}, nil
}

// 订单号生成器  prefix：订单号前缀
func NewOrderNoGenerator(prefix string, workerID int64) (*OrderNoGenerator, error) {
	if prefix == "" {
		return nil, fmt.Errorf("order number prefix is empty")
	}

	sf, err := NewSnowflake(workerID)
	if err != nil {
		return nil, err
	}

	return &OrderNoGenerator{
		prefix:    prefix,
		snowflake: sf,
	}, nil
}

func NewOrderNo(prefix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("order number prefix is empty")
	}

	sf, err := defaultGenerator()
	if err != nil {
		return "", err
	}

	id, err := sf.NextID()
	if err != nil {
		return "", err
	}
	return prefix + strconv.FormatInt(id, 10), nil
}

func (g *OrderNoGenerator) Next() (string, error) {
	id, err := g.snowflake.NextID()
	if err != nil {
		return "", err
	}
	return g.prefix + strconv.FormatInt(id, 10), nil
}

func (s *Snowflake) NextID() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 时钟回拨检测
	nowMillis := currentMillis()
	// 大于5秒就报错，小于5秒等待恢复
	if nowMillis < s.epochMillis {
		return 0, fmt.Errorf("current time is before snowflake epoch")
	}

	if nowMillis < s.lastMillis {
		wait := time.Duration(s.lastMillis-nowMillis) * time.Millisecond
		if wait > maxClockBackward {
			return 0, fmt.Errorf("clock moved backwards by %s", wait)
		}

		time.Sleep(wait)
		nowMillis = currentMillis()
		if nowMillis < s.lastMillis {
			return 0, fmt.Errorf("clock moved backwards by %s", time.Duration(s.lastMillis-nowMillis)*time.Millisecond)
		}
	}

	if nowMillis == s.lastMillis {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			nowMillis = waitNextMillis(s.lastMillis)
		}
	} else {
		s.sequence = 0
	}

	s.lastMillis = nowMillis
	return ((nowMillis - s.epochMillis) << timestampShift) |
		(s.workerID << workerIDShift) |
		s.sequence, nil
}

func defaultGenerator() (*Snowflake, error) {
	defaultSnowflakeOnce.Do(func() { // 只会在第一次调用时执行 //sync.Once.Do() 检测到已经执行过，直接跳过
		workerID, err := defaultWorkerID()
		if err != nil {
			defaultSnowflakeErr = err
			return
		}
		defaultSnowflake, defaultSnowflakeErr = NewSnowflake(workerID)
	})
	return defaultSnowflake, defaultSnowflakeErr
}

func defaultWorkerID() (int64, error) {
	if config.AppConfig != nil && config.AppConfig.IDGen_WorkerID >= 0 {
		workerID := config.AppConfig.IDGen_WorkerID
		if err := validateWorkerID(workerID, "IDGen_WorkerID"); err != nil {
			return 0, err
		}
		return workerID, nil
	}

	raw := strings.TrimSpace(os.Getenv(config.IDGenWorkerIDEnvName))
	if raw != "" {
		// 解析字符串为整数
		workerID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", config.IDGenWorkerIDEnvName, err)
		}
		if err := validateWorkerID(workerID, config.IDGenWorkerIDEnvName); err != nil {
			return 0, err
		}
		return workerID, nil
	}

	return runtimeWorkerID(), nil
}

func validateWorkerID(workerID int64, name string) error {
	if workerID < 0 || workerID > MaxWorkerID {
		return fmt.Errorf("%s must be between 0 and %d", name, MaxWorkerID)
	}
	return nil
}

// 基于主机名和进程PID
func runtimeWorkerID() int64 {
	h := fnv.New32a()
	if hostname, err := os.Hostname(); err == nil {
		_, _ = h.Write([]byte(hostname))
	}

	var pid [4]byte
	//nolint:gosec
	binary.BigEndian.PutUint32(pid[:], uint32(os.Getpid()))
	_, _ = h.Write(pid[:])

	return int64(h.Sum32() & uint32(MaxWorkerID))
}

// 获取当前时间
func currentMillis() int64 {
	return time.Now().UnixMilli()
}

func waitNextMillis(lastMillis int64) int64 {
	nowMillis := currentMillis()
	for nowMillis <= lastMillis {
		time.Sleep(time.Millisecond)
		nowMillis = currentMillis()
	}
	return nowMillis
}
