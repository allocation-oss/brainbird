package cache

import "github.com/redis/go-redis/v9"

func NewClient(address string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: address, PoolSize: 10})
}
