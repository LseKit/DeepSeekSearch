// 文件名: lang.go
// 用途: 校验并规范化 lang 参数，未知 ISO 639-1 码只记 warning 不拒绝。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package toolvalidate

import "strings"

// iso6391Codes 是 ISO 639-1 全量码表；未知码只记 warning，不拒绝。
const iso6391Codes = " aa ab ae af ak am an ar as av ay az ba be bg bh bi bm bn bo br bs ca ce ch co cr cs cu cv cy da de dv dz ee el en eo es et eu fa ff fi fj fo fr fy ga gd gl gn gu gv ha he hi ho hr ht hu hy hz ia id ie ig ii ik io is it iu ja jv ka kg ki kj kk kl km kn ko kr ks ku kv kw ky la lb lg li ln lo lt lu lv mg mh mi mk ml mn mr ms mt my na nb nd ne ng nl nn no nr nv ny oc oj om or os pa pi pl ps pt qu rm rn ro ru rw sa sc sd se sg si sk sl sm sn so sq sr ss st su sv sw ta te tg th ti tk tl tn to tr ts tt tw ty ug uk ur uz ve vi vo wa wo xh yi yo za zh zu "

// NormalizeLangs 去空、转小写；未知码保留并追加 warning。
func NormalizeLangs(langs, warnings []string) (out, outWarnings []string) {
	out = make([]string, 0, len(langs))
	outWarnings = warnings
	for _, lang := range langs {
		code := strings.ToLower(strings.TrimSpace(lang))
		if code == "" {
			continue
		}
		if !strings.Contains(iso6391Codes, " "+code+" ") {
			outWarnings = append(outWarnings, "lang:"+code+" 不是常见的 ISO 639-1 语言码，已按原样发送")
		}
		out = append(out, code)
	}
	return out, outWarnings
}
