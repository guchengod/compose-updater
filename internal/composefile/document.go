package composefile

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/guchengod/compose-updater/internal/atomicfile"
)

type scalarStyle uint8

const (
	plainStyle scalarStyle = iota
	singleQuotedStyle
	doubleQuotedStyle
)

type ImageNode struct {
	Service  string
	Image    string
	Start    int
	End      int
	Style    scalarStyle
	HasBuild bool
}

type Document struct {
	Path     string
	Original []byte
	Mode     os.FileMode
	Images   map[string]ImageNode
}

type Change struct {
	Service  string
	OldImage string
	NewImage string
}

type lineInfo struct {
	start  int
	end    int
	indent int
	text   string
}

type mappingLine struct {
	key        string
	value      string
	valueStart int
	ok         bool
}

type serviceBlock struct {
	name      string
	lineIndex int
}

type patch struct {
	start       int
	end         int
	replacement []byte
}

func Load(path string) (*Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Compose 文件 %q: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读取 Compose 文件属性 %q: %w", path, err)
	}
	lines, err := scanLines(content)
	if err != nil {
		return nil, fmt.Errorf("解析 Compose 文件 %q: %w", path, err)
	}
	servicesLine := -1
	servicesIndent := -1
	for index, line := range lines {
		if isBlankOrComment(line.text) {
			continue
		}
		mapping := parseMapping(line.text)
		if mapping.ok && mapping.key == "services" && strings.TrimSpace(mapping.value) == "" {
			servicesLine = index
			servicesIndent = line.indent
			break
		}
	}
	if servicesLine < 0 {
		return nil, errors.New("缺少块格式的 services: 映射")
	}

	sectionEnd := len(lines)
	for index := servicesLine + 1; index < len(lines); index++ {
		line := lines[index]
		if isBlankOrComment(line.text) {
			continue
		}
		if line.indent <= servicesIndent {
			sectionEnd = index
			break
		}
	}
	serviceIndent := -1
	for index := servicesLine + 1; index < sectionEnd; index++ {
		line := lines[index]
		if isBlankOrComment(line.text) {
			continue
		}
		mapping := parseMapping(line.text)
		if mapping.ok && strings.TrimSpace(mapping.value) == "" {
			if serviceIndent == -1 || line.indent < serviceIndent {
				serviceIndent = line.indent
			}
		}
	}
	if serviceIndent < 0 {
		return &Document{Path: path, Original: content, Mode: info.Mode(), Images: map[string]ImageNode{}}, nil
	}

	var blocks []serviceBlock
	for index := servicesLine + 1; index < sectionEnd; index++ {
		line := lines[index]
		if line.indent != serviceIndent || isBlankOrComment(line.text) {
			continue
		}
		mapping := parseMapping(line.text)
		if !mapping.ok || strings.TrimSpace(mapping.value) != "" {
			continue
		}
		blocks = append(blocks, serviceBlock{name: mapping.key, lineIndex: index})
	}

	images := make(map[string]ImageNode)
	for blockIndex, block := range blocks {
		blockEnd := sectionEnd
		if blockIndex+1 < len(blocks) {
			blockEnd = blocks[blockIndex+1].lineIndex
		}
		fieldIndent := -1
		for index := block.lineIndex + 1; index < blockEnd; index++ {
			line := lines[index]
			if isBlankOrComment(line.text) || line.indent <= serviceIndent {
				continue
			}
			mapping := parseMapping(line.text)
			if mapping.ok && (fieldIndent == -1 || line.indent < fieldIndent) {
				fieldIndent = line.indent
			}
		}
		if fieldIndent < 0 {
			continue
		}
		node := ImageNode{Service: block.name}
		foundImage := false
		for index := block.lineIndex + 1; index < blockEnd; index++ {
			line := lines[index]
			if line.indent != fieldIndent || isBlankOrComment(line.text) {
				continue
			}
			mapping := parseMapping(line.text)
			if !mapping.ok {
				continue
			}
			switch mapping.key {
			case "build":
				node.HasBuild = true
			case "image":
				value, start, end, style, err := parseScalar(line, mapping.valueStart)
				if err != nil {
					return nil, fmt.Errorf("服务 %q image: %w", block.name, err)
				}
				if value != "" {
					node.Image = value
					node.Start = start
					node.End = end
					node.Style = style
					foundImage = true
				}
			}
		}
		if foundImage {
			images[block.name] = node
		}
	}
	return &Document{Path: path, Original: content, Mode: info.Mode(), Images: images}, nil
}

func scanLines(content []byte) ([]lineInfo, error) {
	var lines []lineInfo
	for start := 0; start <= len(content); {
		end := start
		for end < len(content) && content[end] != '\n' {
			end++
		}
		lineEnd := end
		if lineEnd > start && content[lineEnd-1] == '\r' {
			lineEnd--
		}
		textBytes := content[start:lineEnd]
		indent := 0
		for indent < len(textBytes) {
			switch textBytes[indent] {
			case ' ':
				indent++
			case '\t':
				return nil, errors.New("不支持使用 Tab 缩进")
			default:
				goto doneIndent
			}
		}
	doneIndent:
		lines = append(lines, lineInfo{start: start, end: lineEnd, indent: indent, text: string(textBytes)})
		if end >= len(content) {
			break
		}
		start = end + 1
	}
	return lines, nil
}

func parseMapping(line string) mappingLine {
	inSingle := false
	inDouble := false
	escaped := false
	for index := 0; index < len(line); index++ {
		ch := line[index]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if ch == '\'' {
				if index+1 < len(line) && line[index+1] == '\'' {
					index++
					continue
				}
				inSingle = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case ':':
			keyText := strings.TrimSpace(line[:index])
			if keyText == "" || strings.HasPrefix(keyText, "-") {
				return mappingLine{}
			}
			key, ok := decodeKey(keyText)
			if !ok {
				return mappingLine{}
			}
			valueStart := index + 1
			for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t') {
				valueStart++
			}
			return mappingLine{key: key, value: line[valueStart:], valueStart: valueStart, ok: true}
		}
	}
	return mappingLine{}
}

func decodeKey(value string) (string, bool) {
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), true
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		return decoded, err == nil
	}
	if strings.ContainsAny(value, "{}[]#,&*!|>@`\"") {
		return "", false
	}
	return value, true
}

func parseScalar(line lineInfo, valueStart int) (value string, start, end int, style scalarStyle, err error) {
	if valueStart >= len(line.text) {
		return "", 0, 0, plainStyle, nil
	}
	startRelative := valueStart
	start = line.start + startRelative
	text := line.text[startRelative:]
	if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "|") || strings.HasPrefix(text, ">") {
		return "", 0, 0, plainStyle, nil
	}
	switch text[0] {
	case '\'':
		style = singleQuotedStyle
		for index := 1; index < len(text); index++ {
			if text[index] != '\'' {
				continue
			}
			if index+1 < len(text) && text[index+1] == '\'' {
				index++
				continue
			}
			end = start + index + 1
			value = strings.ReplaceAll(text[1:index], "''", "'")
			return value, start, end, style, nil
		}
		return "", 0, 0, style, errors.New("单引号未闭合")
	case '"':
		style = doubleQuotedStyle
		escaped := false
		for index := 1; index < len(text); index++ {
			if escaped {
				escaped = false
				continue
			}
			if text[index] == '\\' {
				escaped = true
				continue
			}
			if text[index] == '"' {
				end = start + index + 1
				decoded, decodeErr := strconv.Unquote(text[:index+1])
				if decodeErr != nil {
					return "", 0, 0, style, decodeErr
				}
				return decoded, start, end, style, nil
			}
		}
		return "", 0, 0, style, errors.New("双引号未闭合")
	default:
		style = plainStyle
		index := 0
		for index < len(text) && text[index] != ' ' && text[index] != '\t' && text[index] != '\r' {
			index++
		}
		if index == 0 {
			return "", 0, 0, style, nil
		}
		value = text[:index]
		end = start + index
		return value, start, end, style, nil
	}
}

func isBlankOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || trimmed == "..."
}

// Rewrite changes only image scalar tokens. It preserves comments, indentation,
// line endings and every unrelated byte in the Compose file.
func (d *Document) Rewrite(changes []Change) (backupPath string, restore func() error, err error) {
	if len(changes) == 0 {
		return "", func() error { return nil }, nil
	}
	patches := make([]patch, 0, len(changes))
	for _, change := range changes {
		node, ok := d.Images[change.Service]
		if !ok {
			return "", nil, fmt.Errorf("服务 %q 没有可直接修改的 image 节点", change.Service)
		}
		if node.Image != change.OldImage {
			return "", nil, fmt.Errorf("服务 %q image 已变化，期望 %q，实际 %q", change.Service, change.OldImage, node.Image)
		}
		patches = append(patches, patch{start: node.Start, end: node.End, replacement: encodeScalar(change.NewImage, node.Style)})
	}
	sort.Slice(patches, func(i, j int) bool { return patches[i].start > patches[j].start })
	updated := append([]byte(nil), d.Original...)
	for _, item := range patches {
		if item.start < 0 || item.end < item.start || item.end > len(updated) {
			return "", nil, errors.New("image 替换范围越界")
		}
		updated = append(updated[:item.start], append(item.replacement, updated[item.end:]...)...)
	}
	backupPath = d.Path + ".compose-updater.bak"
	if err := atomicfile.Write(backupPath, d.Original, d.Mode); err != nil {
		return "", nil, fmt.Errorf("写入 Compose 备份: %w", err)
	}
	if err := atomicfile.Write(d.Path, updated, d.Mode); err != nil {
		return "", nil, fmt.Errorf("写入 Compose 文件: %w", err)
	}
	restore = func() error { return atomicfile.Write(d.Path, d.Original, d.Mode) }
	return backupPath, restore, nil
}

func encodeScalar(value string, style scalarStyle) []byte {
	switch style {
	case singleQuotedStyle:
		return []byte("'" + strings.ReplaceAll(value, "'", "''") + "'")
	case doubleQuotedStyle:
		return []byte(strconv.Quote(value))
	default:
		return []byte(value)
	}
}
