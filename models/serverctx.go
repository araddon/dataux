package models

import (
	"fmt"

	u "github.com/araddon/gou"

	"github.com/araddon/qlbridge/datasource"
	"github.com/araddon/qlbridge/plan"
	"github.com/araddon/qlbridge/schema"

	"github.com/dataux/dataux/planner"
)

// ServerCtx Singleton global Context for the DataUX Server giving
// access to the shared Config, Schemas, Grid runtime
type ServerCtx struct {
	// The dataux server config info on schema, backends, frontends, etc
	Config *Config

	// The underlying qlbridge registry holds info about the available datasource providers
	Reg *datasource.Registry

	// PlanGrid is swapping out the qlbridge planner
	// with a distributed version that uses Grid lib to split
	// tasks across nodes
	PlanGrid *planner.PlannerGrid

	schemas map[string]*schema.Schema
}

func NewServerCtx(conf *Config) *ServerCtx {
	svr := ServerCtx{}
	svr.Config = conf
	svr.Reg = datasource.DataSourcesRegistry()
	return &svr
}

// Init Load all the config info for this server and start the
// grid/messaging/coordination systems
func (m *ServerCtx) Init() error {

	if err := m.loadConfig(); err != nil {
		return err
	}

	m.Reg.Init()

	for _, s := range m.Reg.Schemas() {
		if _, exists := m.schemas[s]; !exists {
			// new from init
			sch, _ := m.Reg.Schema(s)
			if sch != nil {
				m.schemas[s] = sch
			}
		}
	}

	// Copy over the nats, etcd info from config to
	// Planner grid
	planner.GridConf.EtcdServers = m.Config.Etcd

	// how many worker nodes?
	if m.Config.WorkerCt == 0 {
		m.Config.WorkerCt = 2
	}

	m.PlanGrid = planner.NewPlannerGrid(m.Config.WorkerCt, m.Reg)

	return nil
}

// SchemaLoader finds a schema by name from the registry
func (m *ServerCtx) SchemaLoader(db string) (*schema.Schema, error) {
	s, ok := m.Reg.Schema(db)
	if s == nil || !ok {
		u.Warnf("Could not find schema for db=%s", db)
		return nil, schema.ErrNotFound
	}
	return s, nil
}

// Get A schema
func (m *ServerCtx) InfoSchema() (*schema.Schema, error) {
	if len(m.schemas) == 0 {
		for _, sc := range m.Config.Schemas {
			s, ok := m.Reg.Schema(sc.Name)
			if s != nil && ok {
				u.Warnf("%p found schema for db=%q", m, sc.Name)
				return s, nil
			}
		}
		return nil, schema.ErrNotFound
	}
	for _, s := range m.schemas {
		return s, nil
	}
	panic("unreachable")
}

func (m *ServerCtx) JobMaker(ctx *plan.Context) (*planner.ExecutorGrid, error) {
	//u.Debugf("jobMaker, going to do a partial plan?")
	return planner.BuildExecutorUnPlanned(ctx, m.PlanGrid)
}

// Table Get by schema, name
func (m *ServerCtx) Table(schemaName, tableName string) (*schema.Table, error) {
	s, ok := m.schemas[schemaName]
	if ok {
		return s.Table(tableName)
	}
	return nil, fmt.Errorf("That schema %q not found", schemaName)
}

func (m *ServerCtx) loadConfig() error {

	m.schemas = make(map[string]*schema.Schema)

	u.Debugf("server load config schema ct=%d", len(m.schemas))

	for _, schemaConf := range m.Config.Schemas {

		//u.Debugf("parse schemas: %v", schemaConf)
		if _, ok := m.schemas[schemaConf.Name]; ok {
			panic(fmt.Sprintf("duplicate schema '%s'", schemaConf.Name))
		}

		sch := schema.NewSchema(schemaConf.Name)
		m.Reg.SchemaAdd(sch)

		// find the Source config for eached named db/source
		for _, sourceName := range schemaConf.Sources {

			var sourceConf *schema.ConfigSource
			// we must find a source conf by name
			for _, sc := range m.Config.Sources {
				//u.Debugf("sc: %s %#v", sourceName, sc)
				if sc.Name == sourceName {
					sourceConf = sc
					break
				}
			}
			if sourceConf == nil {
				u.Warnf("could not find source: %v", sourceName)
				return fmt.Errorf("Could not find Source Config for %v", sourceName)
			}

			//u.Debugf("new Source: %s   %+v", sourceName, sourceConf)
			ss := schema.NewSchemaSource(sourceName, sourceConf.SourceType)
			ss.Conf = sourceConf
			//u.Infof("found sourceName: %q schema.Name=%q conf=%+v", sourceName, ss.Name, sourceConf)

			if len(m.Config.Nodes) == 0 {
				for _, host := range sourceConf.Hosts {
					nc := &schema.ConfigNode{Source: sourceName, Address: host}
					//ss.Nodes = append(ss.Nodes, nc)
					sourceConf.Nodes = append(sourceConf.Nodes, nc)
				}
			} else {
				for _, nc := range m.Config.Nodes {
					if nc.Source == sourceConf.Name {
						//ss.Nodes = append(ss.Nodes, nc)
						sourceConf.Nodes = append(sourceConf.Nodes, nc)
					}
				}
			}

			//u.Debugf("s:%p  ss:%p  adding ss", sch, ss)
			sch.AddSourceSchema(ss)
			//u.Debug("after add source schema")

			ds := m.Reg.Get(sourceConf.SourceType)
			//u.Debugf("after reg.Get(%q)  %#v", sourceConf.SourceType, ds)
			if ds == nil {
				//u.Warnf("could not find source for %v  %v", sourceName, sourceConf.SourceType)
			} else {
				ss.DS = ds
				ss.Partitions = sourceConf.Partitions
				//u.Debugf("about to Setup %#v", ds)
				if err := ss.DS.Setup(ss); err != nil {
					u.Errorf("Error setuping up %v  %v", sourceName, err)
				}
				//u.Infof("about to SourceSchemaAdd")
				m.Reg.SourceSchemaAdd(sch.Name, ss)
				//u.Infof("after source schema add")
			}
		}

		// Now refresh the schema, ie load meta-data about the now
		// defined sub-schemas
		sch.RefreshSchema()
	}

	return nil
}

func (m *ServerCtx) Schema(source string) (*schema.Schema, bool) {
	return m.Reg.Schema(source)
}
