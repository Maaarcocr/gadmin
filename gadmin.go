// gadmin
package gadmin

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"
	"unicode"

	"github.com/fatih/structs"
	"github.com/gorilla/mux"
	"github.com/jinzhu/gorm"
)

type data map[string]interface{}
type dataCollection []data
type context map[string]dataCollection

type Gadmin struct {
	db              *gorm.DB
	resources       map[string]interface{}
	resourcesFields map[string][]string
	ctx             context
	mx              *mux.Router
	authF           func(http.Handler) http.Handler
	auth            bool
}

func New(db *gorm.DB, mx *mux.Router) Gadmin {
	return Gadmin{
		resources:       make(map[string]interface{}),
		resourcesFields: make(map[string][]string),
		db:              db,
		mx:              mx,
	}
}

func (g *Gadmin) AddResource(r interface{}, filtered ...string) {
	typeOfResource := reflect.TypeOf(r)
	name := typeOfResource.Name()
	g.resources[name] = r
	mapOfStruct := structs.Map(r)
	flatMap(mapOfStruct)
	fields := make([]string, 0)
	for key, _ := range mapOfStruct {
		fields = append(fields, key)
	}
	g.resourcesFields[name] = fields
}

func toSnake(in string) string {
	runes := []rune(in)
	length := len(runes)
	var out []rune
	for i := 0; i < length; i++ {
		if i > 0 && unicode.IsUpper(runes[i]) && ((i+1 < length && unicode.IsLower(runes[i+1])) || unicode.IsLower(runes[i-1])) {
			out = append(out, '_')
		}
		out = append(out, unicode.ToLower(runes[i]))
	}

	return string(out)
}

func getValueFromResource(r interface{}) reflect.Value {
	slice := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(r)), 0, 0)
	x := reflect.New(slice.Type())
	x.Elem().Set(slice)
	return x
}

//remove Model map inside the map
func flatMap(m map[string]interface{}) {
	if _, okay := m["Model"]; okay {
		mapModel, _ := m["Model"].(map[string]interface{})
		for key, val := range mapModel {
			m[key] = val
		}
	}
	delete(m, "Model")
}

func getDataFromDb(key string, r interface{}, db *gorm.DB) dataCollection {
	keySnake := toSnake(key) + "s"
	x := getValueFromResource(r)
	db.Table(keySnake).Scan(x.Interface())
	result := make(dataCollection, 0)
	for i := 0; i < x.Elem().Len(); i++ {
		mapped := structs.Map(x.Elem().Index(i).Interface())
		flatMap(mapped)
		result = append(result, mapped)
	}
	return result
}

func getRootDir() string {
	gopath := os.Getenv("GOPATH")
	return filepath.Join(gopath, "src", "github.com", "Maaarcocr", "gadmin")
}

func inList(list []string, str string) bool {
	for _, s := range list {
		if s == str {
			return true
		}
	}
	return false
}

func format(in interface{}) interface{} {
	if date, okay := in.(time.Time); okay {
		return date.Format("02/01/2006 15:04:05")
	} else if date, okay := in.(*time.Time); okay {
		if date == nil {
			return ""
		}
		return date.Format("02/01/2006 15:04:05")
	} else if dur, okay := in.(time.Duration); okay {
		return dur.String()
	} else {
		return in
	}
}

func deletedFilter(in interface{}) bool {
	if date, okay := in.(*time.Time); okay {
		if date == nil {
			return false
		}
		return true
	}
	return false
}

func getTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"in":            inList,
		"format":        format,
		"deletedFilter": deletedFilter,
	}
}

func getPage(g Gadmin) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		table := mux.Vars(r)["collection"]
		if _, okay := g.resources[table]; !okay {
			http.Error(w, "This table doesn't exist or we don't know it exists", http.StatusBadRequest)
		}
		htmlFileName := filepath.Join(getRootDir(), "templates/template.html")
		t, err := template.New("template.html").Funcs(getTemplateFuncs()).ParseFiles(htmlFileName)
		if err != nil {
			fmt.Println("err parsing: ", err)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		t.Execute(w, g.getContext(table))
	}
	return http.HandlerFunc(fn)
}

func (g *Gadmin) getPagesList() []string {
	result := make([]string, 0)
	for key, _ := range g.resourcesFields {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (g *Gadmin) getContext(table string) map[string]interface{} {
	g.updateCtx()
	return map[string]interface{}{
		"Fields":   g.resourcesFields[table],
		"Context":  g.ctx[table],
		"Filter":   []string{"Password", "Modules", "DeletedAt"},
		"Pages":    g.getPagesList(),
		"PageName": table,
	}
}

func (g *Gadmin) updateCtx() {
	ctx := make(context)
	for key, val := range g.resources {
		ctx[key] = getDataFromDb(key, val, g.db)
	}
	g.ctx = ctx
}

func deleteFromDatabase(g Gadmin) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		table := mux.Vars(r)["collection"]
		id := mux.Vars(r)["id"]
		if _, okay := g.resources[table]; !okay {
			http.Error(w, "This table doesn't exist or we don't know it exists", http.StatusBadRequest)
		}
		g.db.Table(toSnake(table)+"s").Where("id = ?", id).Delete(g.resources[table])
	}
	return http.HandlerFunc(fn)
}

func edit(g Gadmin) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		table := mux.Vars(r)["collection"]
		id := mux.Vars(r)["id"]
		if _, okay := g.resources[table]; !okay {
			http.Error(w, "This table doesn't exist or we don't know it exists", http.StatusBadRequest)
		}
		r.ParseForm()
		var updateMap map[string]interface{} = make(map[string]interface{})
		for k, value := range r.Form {
			key := toSnake(k)
			v := reflect.ValueOf(g.resources[table])
			field := v.FieldByName(k).Interface()
			if _, okay := field.(bool); okay {
				fmt.Println("bool")
				var err error
				updateMap[key], err = strconv.ParseBool(value[0])
				if err != nil {
					http.Error(w, "We were excpecting a boolean for the field: "+key, http.StatusBadRequest)
					return
				}
			} else if _, okay := field.(int); okay {
				var err error
				updateMap[key], err = strconv.Atoi(value[0])
				if err != nil {
					http.Error(w, "We were excpecting an integer for the field: "+key, http.StatusBadRequest)
					return
				}
			} else if _, okay := field.(float64); okay {
				var err error
				updateMap[key], err = strconv.ParseFloat(value[0], 64)
				if err != nil {
					http.Error(w, "We were excpecting a float for the field: "+key, http.StatusBadRequest)
					return
				}
			} else if _, okay := field.(time.Time); okay {
				var err error
				updateMap[key], err = time.Parse("02/01/2006 15:04:05", value[0])
				if err != nil {
					http.Error(w, "We were excpecting a date in the format: 02/01/2006 15:04:05 for the field: "+key, http.StatusBadRequest)
					return
				}
			} else {
				updateMap[key] = value[0]
			}
			fmt.Println(key, updateMap[key])
		}
		//model := g.resources[table]
		g.db.Debug().Table(toSnake(table)+"s").Where("id = ?", id).Update(updateMap)
	}
	return http.HandlerFunc(fn)
}

func (g *Gadmin) SetAuth(f func(http.Handler) http.Handler) {
	g.auth = true
	g.authF = f
}

func (g *Gadmin) Run() {
	g.updateCtx()
	if g.auth == true {
		g.mx.Handle("/admin/manager/{collection}", g.authF(getPage(*g))).Methods("GET")
		g.mx.Handle("/admin/manager/{collection}/edit/{id}", g.authF(edit(*g))).Methods("POST")
		g.mx.Handle("/admin/manager/{collection}/delete/{id}", g.authF(deleteFromDatabase(*g))).Methods("DELETE")
	} else {
		g.mx.Handle("/admin/manager/{collection}", getPage(*g)).Methods("GET")
		g.mx.Handle("/admin/manager/{collection}/edit/{id}", edit(*g)).Methods("POST")
		g.mx.Handle("/admin/manager/{collection}/delete/{id}", deleteFromDatabase(*g)).Methods("DELETE")
	}
}
