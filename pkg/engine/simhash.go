package engine

import (
	"hash/fnv"
	"net/url"
	"strings"
	"sync"
)

// SimHash URL 相似度去重
type SimHash struct {
	mu        sync.RWMutex
	hashes    map[uint64]string // hash -> 原始 URL
	threshold int               // 汉明距离阈值
	maxSize   int               // 最大缓存大小
}

// NewSimHash 创建 SimHash 去重器
func NewSimHash(threshold, maxSize int) *SimHash {
	if threshold <= 0 {
		threshold = 3 // 默认汉明距离阈值
	}
	if maxSize <= 0 {
		maxSize = 100000
	}
	return &SimHash{
		hashes:    make(map[uint64]string),
		threshold: threshold,
		maxSize:   maxSize,
	}
}

// Hash 计算 URL 的 SimHash
func (s *SimHash) Hash(urlStr string) uint64 {
	tokens := tokenizeURL(urlStr)
	if len(tokens) == 0 {
		return 0
	}

	// 计算每个 token 的哈希并累加到 64 位向量
	var v [64]int
	for _, token := range tokens {
		h := hash64(token)
		for i := 0; i < 64; i++ {
			if (h>>i)&1 == 1 {
				v[i]++
			} else {
				v[i]--
			}
		}
	}

	// 生成最终的 SimHash
	var simhash uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			simhash |= 1 << i
		}
	}

	return simhash
}

// IsSimilar 检查 URL 是否与已存在的 URL 相似
// 返回是否相似，以及相似的原始 URL（如果存在）
func (s *SimHash) IsSimilar(urlStr string) (bool, string) {
	h := s.Hash(urlStr)

	s.mu.RLock()
	for existing, originalURL := range s.hashes {
		if hammingDistance(h, existing) <= s.threshold {
			s.mu.RUnlock()
			return true, originalURL
		}
	}
	s.mu.RUnlock()

	// 添加到缓存
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查缓存大小
	if len(s.hashes) >= s.maxSize {
		// 清理一半
		count := 0
		limit := len(s.hashes) / 2
		for k := range s.hashes {
			if count >= limit {
				break
			}
			delete(s.hashes, k)
			count++
		}
	}

	s.hashes[h] = urlStr
	return false, ""
}

// Add 添加 URL 到缓存（不检查相似度）
func (s *SimHash) Add(urlStr string) {
	h := s.Hash(urlStr)

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.hashes) >= s.maxSize {
		// 清理一半
		count := 0
		limit := len(s.hashes) / 2
		for k := range s.hashes {
			if count >= limit {
				break
			}
			delete(s.hashes, k)
			count++
		}
	}

	s.hashes[h] = urlStr
}

// Size 返回缓存大小
func (s *SimHash) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.hashes)
}

// Clear 清空缓存
func (s *SimHash) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes = make(map[uint64]string)
}

// tokenizeURL 将 URL 分词
func tokenizeURL(urlStr string) []string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	var tokens []string

	// 添加 host
	if parsed.Host != "" {
		tokens = append(tokens, strings.ToLower(parsed.Host))
	}

	// 分解 path
	path := parsed.Path
	// 移除扩展名（如 .html, .php 等）
	if idx := strings.LastIndex(path, "."); idx > 0 {
		ext := path[idx:]
		if len(ext) <= 5 { // 合理的扩展名长度
			path = path[:idx]
		}
	}

	// 按分隔符分割
	pathTokens := splitByAny(path, "/.-_")
	for _, t := range pathTokens {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" && !isNumeric(t) && len(t) < 50 {
			tokens = append(tokens, t)
		}
	}

	// 分解查询参数（只取参数名，忽略值）
	for key := range parsed.Query() {
		k := strings.ToLower(key)
		if k != "" && len(k) < 50 {
			tokens = append(tokens, k)
		}
	}

	return tokens
}

// splitByAny 按多个分隔符分割字符串
func splitByAny(s string, seps string) []string {
	var tokens []string
	var current strings.Builder

	for _, c := range s {
		if strings.ContainsRune(seps, c) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(c)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// isNumeric 检查字符串是否全是数字
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// hash64 计算字符串的 64 位哈希
func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// hammingDistance 计算两个 64 位整数的汉明距离
func hammingDistance(a, b uint64) int {
	xor := a ^ b
	count := 0
	for xor != 0 {
		count++
		xor &= xor - 1
	}
	return count
}

// URLSimilarityChecker URL 相似度检查器
// 结合精确匹配和 SimHash 模糊匹配
type URLSimilarityChecker struct {
	exact   map[string]bool
	simhash *SimHash
	mu      sync.RWMutex
}

// NewURLSimilarityChecker 创建 URL 相似度检查器
func NewURLSimilarityChecker(simhashThreshold, maxSize int) *URLSimilarityChecker {
	return &URLSimilarityChecker{
		exact:   make(map[string]bool),
		simhash: NewSimHash(simhashThreshold, maxSize),
	}
}

// IsExistOrSimilar 检查 URL 是否已存在或与已存在的 URL 相似
// 返回：是否应该跳过，原因
func (c *URLSimilarityChecker) IsExistOrSimilar(urlStr string) (bool, string) {
	// 先检查精确匹配
	c.mu.RLock()
	if c.exact[urlStr] {
		c.mu.RUnlock()
		return true, "exact_match"
	}
	c.mu.RUnlock()

	// 检查 SimHash 相似度
	similar, originalURL := c.simhash.IsSimilar(urlStr)
	if similar {
		return true, "similar_to:" + originalURL
	}

	// 添加到精确匹配缓存
	c.mu.Lock()
	c.exact[urlStr] = true
	c.mu.Unlock()

	return false, ""
}

// Add 添加 URL（不检查）
func (c *URLSimilarityChecker) Add(urlStr string) {
	c.mu.Lock()
	c.exact[urlStr] = true
	c.mu.Unlock()

	c.simhash.Add(urlStr)
}

// Stats 返回统计信息
func (c *URLSimilarityChecker) Stats() map[string]int {
	c.mu.RLock()
	exactSize := len(c.exact)
	c.mu.RUnlock()

	return map[string]int{
		"exact_cache_size":   exactSize,
		"simhash_cache_size": c.simhash.Size(),
	}
}
