// 文件名: ipv4.go
// 用途: 把 1~4 段的混淆 IPv4 写法（十/八/十六进制）合成为 uint32。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

// CombineIPv4 把 1~4 段的十进制/八进制/十六进制写法合成 IPv4。
func CombineIPv4(values []uint64) (uint32, bool) {
	if len(values) == 1 {
		if values[0] > 0xFFFFFFFF {
			return 0, false
		}
		return uint32(values[0]), true //nolint:gosec // 已校验 ≤0xFFFFFFFF
	}
	widths, ok := ipv4Widths(len(values))
	if !ok {
		return 0, false
	}
	var out uint32
	for i, width := range widths {
		if values[i] > (uint64(1)<<width)-1 {
			return 0, false
		}
		out = out<<width | uint32(values[i]) //nolint:gosec // 已按 width 校验上界
	}
	return out, true
}

func ipv4Widths(n int) ([]uint, bool) {
	switch n {
	case 2:
		return []uint{8, 24}, true
	case 3:
		return []uint{8, 8, 16}, true
	case 4:
		return []uint{8, 8, 8, 8}, true
	default:
		return nil, false
	}
}
