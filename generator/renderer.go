package generator

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"io"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/huandu/xstrings"
)

// Creator provices the functionality to create a file
type Creator interface {
	Create(name string) (io.WriteCloser, error)
}

// Renderer handles the process to render a Root to a Creator interface
type Renderer struct {
	tmpl *template.Template
	root *Root

	fs Creator
}

//go:embed templates/*.tmpl
var templates embed.FS

// NewRenderer initializes a renderer
func NewRenderer(fs Creator, root *Root) (*Renderer, error) {
	r := &Renderer{
		root: root,
		fs:   fs,
	}
	tmpl, err := template.New("base").ParseFS(templates, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	r.tmpl = tmpl
	return r, nil
}

var doNotEdit = "// Code generated by go-plotly/generator. DO NOT EDIT."

// CreateTrace creates a file with the content of a trace by name
func (r *Renderer) CreateTrace(dir string, name string) error {
	src := &bytes.Buffer{}
	err := r.WriteTrace(name, src)
	if err != nil {
		return err
	}

	fmtsrc, err := format.Source(src.Bytes())
	if err != nil {
		return fmt.Errorf("cannot format source, %w", err)
	}

	file, err := r.fs.Create(path.Join(dir, name+"_gen.go"))
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(fmtsrc)
	if err != nil {
		return fmt.Errorf("cannot write source, %w", err)
	}

	return nil
}

// WriteTrace writes a trace by name to a writer
func (r *Renderer) WriteTrace(traceName string, w io.Writer) error {
	trace := r.root.Schema.Traces[traceName]

	traceFile := typeFile{
		MainType: sstruct{
			Name:        xstrings.ToCamelCase(trace.Type),
			Description: trace.Meta.Description,
			Fields: []structField{
				{
					Name:        "Type",
					JSONName:    "type",
					Type:        "TraceType",
					Description: []string{"is the type of the plot"},
				},
			},
		},
		Objects:   []sstruct{},
		Enums:     []enumFile{},
		FlagLists: []flagList{},
	}

	fields, err := traceFile.parseAttributes(traceFile.MainType.Name, traceFile.MainType.Name, trace.Attributes.Names)
	if err != nil {
		return fmt.Errorf("cannot parse attributes, %w", err)
	}
	traceFile.MainType.Fields = append(traceFile.MainType.Fields, fields...)

	fmt.Fprintf(w, `package grob

%s

var TraceType%s TraceType = "%s"

func (trace *%s) GetType() TraceType {
	return TraceType%s
}
`,
		doNotEdit,
		traceFile.MainType.Name,
		traceName,
		traceFile.MainType.Name,
		traceFile.MainType.Name,
	)

	err = r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.MainType)
	if err != nil {
		return err
	}
	for i := range traceFile.Objects {
		err := r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.Objects[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.Enums {
		err := r.tmpl.ExecuteTemplate(w, "enum.tmpl", traceFile.Enums[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.FlagLists {
		err := r.tmpl.ExecuteTemplate(w, "flaglist.tmpl", traceFile.FlagLists[i])
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateTraces creates all traces in the given directory
func (r *Renderer) CreateTraces(dir string) error {
	traceNames := make([]string, 0, len(r.root.Schema.Traces))
	for n := range r.root.Schema.Traces {
		traceNames = append(traceNames, n)
	}
	sort.Strings(traceNames)
	for _, name := range traceNames {
		err := r.CreateTrace(dir, name)
		if err != nil {
			return fmt.Errorf("cannot create trace, %w", err)
		}
	}
	return nil
}

// CreateLayout creates the layout file in the given directory
func (r *Renderer) CreateLayout(dir string) error {
	src := &bytes.Buffer{}
	err := r.WriteLayout(src)
	if err != nil {
		return err
	}

	fmtsrc, err := format.Source(src.Bytes())
	if err != nil {
		return fmt.Errorf("cannot format source, %w", err)
	}

	file, err := r.fs.Create(path.Join(dir, "layout_gen.go"))
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(fmtsrc)
	if err != nil {
		return fmt.Errorf("cannot write source, %w", err)
	}

	return nil
}

// WriteLayout writes layout to the given writer
func (r *Renderer) WriteLayout(w io.Writer) error {
	traceFile := typeFile{
		MainType: sstruct{
			Name:        "Layout",
			Description: "Plot layout options",
			Fields:      []structField{},
		},
		Objects:   []sstruct{},
		Enums:     []enumFile{},
		FlagLists: []flagList{},
	}

	fields, err := traceFile.parseAttributes(traceFile.MainType.Name, traceFile.MainType.Name, r.root.Schema.Layout.LayoutAttributes.Names)
	if err != nil {
		return fmt.Errorf("cannot parse attributes, %w", err)
	}
	traceFile.MainType.Fields = append(traceFile.MainType.Fields, fields...)

	for name, trace := range r.root.Schema.Traces {
		fields, err := traceFile.parseAttributes(xstrings.ToCamelCase(name), "Layout", trace.LayoutAttributes.Names)
		if err != nil {
			return fmt.Errorf("cannot parse attributes, %w", err)
		}
		traceFile.MainType.Fields = append(traceFile.MainType.Fields, fields...)
	}

	sort.Sort(traceFile.MainType.Fields)
	sort.Sort(traceFile.Enums)

	// remove duplicate fields
	uniqueFields := make([]structField, 0, len(traceFile.MainType.Fields))
	fieldMap := map[string]int{}
	for i, field := range traceFile.MainType.Fields {
		_, ok := fieldMap[field.Name]
		if !ok {
			fieldMap[field.Name] = i
			uniqueFields = append(uniqueFields, field)
			continue
		}
	}
	traceFile.MainType.Fields = uniqueFields

	// merge duplicate enums
	uniqueEnums := make([]enumFile, 0, len(traceFile.Enums))
	enumMap := map[string]int{}
	for _, enum := range traceFile.Enums {
		previous, ok := enumMap[enum.Name]
		if !ok {
			uniqueEnums = append(uniqueEnums, enum)
			enumMap[enum.Name] = len(uniqueEnums) - 1
			continue
		}
		uniqueEnums[previous].Values = append(uniqueEnums[previous].Values, enum.Values...)
	}
	traceFile.Enums = uniqueEnums

	// add multiple x and y axis
	for _, label := range []string{"X", "Y"} {
		for i := 2; i < 7; i++ {
			traceFile.MainType.Fields = append(traceFile.MainType.Fields, structField{
				Name:        fmt.Sprintf("%sAxis%d", label, i),
				Description: []string{fmt.Sprintf("%s Axis number %d", label, i)},
				JSONName:    strings.ToLower(fmt.Sprintf("%saxis%d", label, i)),
				Type:        fmt.Sprintf("Layout%saxis", label),
			})
		}
	}

	fmt.Fprint(w, `package grob

`, doNotEdit)

	err = r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.MainType)
	if err != nil {
		return err
	}
	for i := range traceFile.Objects {
		err := r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.Objects[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.Enums {
		err := r.tmpl.ExecuteTemplate(w, "enum.tmpl", traceFile.Enums[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.FlagLists {
		err := r.tmpl.ExecuteTemplate(w, "flaglist.tmpl", traceFile.FlagLists[i])
		if err != nil {
			return err
		}
	}
	return nil

}

// CreateConfig creates the config file in the given director
func (r *Renderer) CreateConfig(dir string) error {
	src := &bytes.Buffer{}
	err := r.WriteConfig(src)
	if err != nil {
		return err
	}

	fmtsrc, err := format.Source(src.Bytes())
	if err != nil {
		return fmt.Errorf("cannot format source, %w", err)
	}

	file, err := r.fs.Create(path.Join(dir, "config_gen.go"))
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(fmtsrc)
	if err != nil {
		return fmt.Errorf("cannot write source, %w", err)
	}

	return nil
}

// WriteConfig writes config to the given writer
func (r *Renderer) WriteConfig(w io.Writer) error {
	traceFile := typeFile{
		MainType: sstruct{
			Name:        "Config",
			Description: "Plot config options",
			Fields:      []structField{},
		},
		Objects:   []sstruct{},
		Enums:     []enumFile{},
		FlagLists: []flagList{},
	}
	fields, err := traceFile.parseAttributes(traceFile.MainType.Name, traceFile.MainType.Name, r.root.Schema.Config.Names)
	if err != nil {
		return fmt.Errorf("cannot parse attributes, %w", err)
	}
	traceFile.MainType.Fields = append(traceFile.MainType.Fields, fields...)

	fmt.Fprint(w, `package grob

`, doNotEdit)

	err = r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.MainType)
	if err != nil {
		return err
	}
	for i := range traceFile.Objects {
		err := r.tmpl.ExecuteTemplate(w, "trace.tmpl", traceFile.Objects[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.Enums {
		err := r.tmpl.ExecuteTemplate(w, "enum.tmpl", traceFile.Enums[i])
		if err != nil {
			return err
		}
	}
	for i := range traceFile.FlagLists {
		err := r.tmpl.ExecuteTemplate(w, "flaglist.tmpl", traceFile.FlagLists[i])
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateUnmarshal creates the unmarshal file on the given directory
func (r *Renderer) CreateUnmarshal(dir string) error {
	src := &bytes.Buffer{}
	err := r.WriteUnmarshal(src)
	if err != nil {
		return err
	}

	fmtsrc, err := format.Source(src.Bytes())
	if err != nil {
		return fmt.Errorf("cannot format source, %w", err)
	}

	file, err := r.fs.Create(path.Join(dir, "unmarshal_gen.go"))
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(fmtsrc)
	if err != nil {
		return fmt.Errorf("cannot write source, %w", err)
	}

	return nil
}

// WriteUnmarshal writes unmarshal to the given writer
func (r *Renderer) WriteUnmarshal(w io.Writer) error {
	file := unmarshalFile{
		Types: make([]string, 0, len(r.root.Schema.Traces)),
	}

	for trace := range r.root.Schema.Traces {
		file.Types = append(file.Types, xstrings.ToCamelCase(trace))
	}
	sort.Strings(file.Types)

	return r.tmpl.ExecuteTemplate(w, "unmarshal.tmpl", file)
}

// unmarshalFile is a structure used to render unmarshal.tmpl
type unmarshalFile struct {
	Types []string
}

// valTypeMap maps between ValTypes and go types
var valTypeMap = map[ValType]string{
	ValTypeDataArray:  "interface{}",
	ValTypeEnum:       "NO-TYPE",
	ValTypeBoolean:    "Bool",
	ValTypeNumber:     "float64",
	ValTypeInteger:    "int64",
	ValTypeString:     "String",
	ValTypeColor:      "Color",
	ValTypeColorlist:  "ColorList",
	ValTypeColorscale: "ColorScale",
	ValTypeAngle:      "float64",
	ValTypeSubplotID:  "String",
	ValTypeFlagList:   "NO-TYPE",
	ValTypeAny:        "interface{}",
	ValTypeInfoArray:  "interface{}",
}

// symbolMap translates a symbol into valid go identifier
var symbolMap = []string{
	"=", "Eq",
	">", "Gt",
	"-", "Hyphen",
	"<", "Lt",
	"|", "Or",
	"/", "Slash",
	"\\", "Doublebackslash",
	"^", "Cape",
	"(", "Lpar",
	")", "Rpar",
	"[", "Lbracket",
	"]", "Rbracket",
	"+", "Plus",
	"?", "Question",
	"$", "Dollar",
}

func cleanName(name string) string {
	replacer := strings.NewReplacer(symbolMap...)
	return replacer.Replace(name)
}
func cleanValue(value string) string {
	return strings.ReplaceAll(value, "\\", "\\\\")
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
