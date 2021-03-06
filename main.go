package main

import (
     "database/sql"
     "flag"
     "net"
     "net/http"
     "strconv"
     "time"
     "fmt"

    //"io/ioutil"
    //"gopkg.in/yaml.v2"
     _ "github.com/mattn/go-oci8"
     "github.com/prometheus/client_golang/prometheus"
     "github.com/prometheus/client_golang/prometheus/promhttp"
     "github.com/prometheus/common/log"
)

// Metric name parts.
const (
     namespace = "oracledb"
     exporter  = "exporter"
)

// Exporter collects Oracle DB metrics. It implements prometheus.Collector.
type Exporter struct {
    duration, error *prometheus.GaugeVec
    totalScrapes    *prometheus.CounterVec
    scrapeErrors    *prometheus.CounterVec
     session         *prometheus.GaugeVec
     sysstat         *prometheus.GaugeVec
     waitclass       *prometheus.GaugeVec
     sysmetric       *prometheus.GaugeVec
     interconnect    *prometheus.GaugeVec
     uptime          *prometheus.GaugeVec
     up              *prometheus.GaugeVec
     tablespace      *prometheus.GaugeVec
     recovery        *prometheus.GaugeVec
     redo            *prometheus.GaugeVec
     cache           *prometheus.GaugeVec
     alertlog        *prometheus.GaugeVec
     alertdate       *prometheus.GaugeVec
     services        *prometheus.GaugeVec
     parameter       *prometheus.GaugeVec
     //query           *prometheus.GaugeVec
     asmspace   *prometheus.GaugeVec
	 //config          Config
	 configs     []*Config
     tablerows  *prometheus.GaugeVec
     tablebytes *prometheus.GaugeVec
     indexbytes *prometheus.GaugeVec
     lobbytes   *prometheus.GaugeVec
     lastIp     string
     vTabRows   bool
     vTabBytes  bool
     vIndBytes  bool
     vLobBytes  bool
     vRecovery  bool
     custom     map[string]*prometheus.GaugeVec
}

var (
     // Version will be set at build time.
     Version       = "1.1.6"
     listenAddress = flag.String("web.listen-address", ":9161", "Address to listen on for web interface and telemetry.")
     metricPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
     pMetrics      = flag.Bool("defaultmetrics", true, "Expose standard metrics")
     pTabRows      = flag.Bool("tablerows", false, "Expose Table rows (CAN TAKE VERY LONG)")
     pTabBytes     = flag.Bool("tablebytes", false, "Expose Table size (CAN TAKE VERY LONG)")
     pIndBytes     = flag.Bool("indexbytes", false, "Expose Index size for any Table (CAN TAKE VERY LONG)")
     pLobBytes     = flag.Bool("lobbytes", false, "Expose Lobs size for any Table (CAN TAKE VERY LONG)")
    pNoRownum     = flag.Bool("norownum", false, "omit rownum label in custom metrics")
     pRecovery     = flag.Bool("recovery", false, "Expose Recovery percentage usage of FRA (CAN TAKE VERY LONG)")
     configFile    = flag.String("configfile", "oracle.conf", "ConfigurationFile in YAML format.")
     logFile       = flag.String("logfile", "exporter.log", "Logfile for parsed Oracle Alerts.")
     accessFile    = flag.String("accessfile", "access.conf", "Last access for parsed Oracle Alerts.")
     landingPage   = []byte(`<html>
                          <head><title>Prometheus Oracle exporter</title></head>
                          <body>
                            <h1>Prometheus Oracle exporter</h1><p>
                            <a href='` + *metricPath + `'>Metrics</a></p>
                            <a href='` + *metricPath + `?target=database name'>Metrics only one database (all instances will be scraped)</a></p>
                            <a href='` + *metricPath + `?tablerows=true'>Metrics with tablerows</a></p>
                            <a href='` + *metricPath + `?tablebytes=true'>Metrics with tablebytes</a></p>
                            <a href='` + *metricPath + `?indexbytes=true'>Metrics with indexbytes</a></p>
                            <a href='` + *metricPath + `?lobbytes=true'>Metrics with lobbytes</a></p>
                            <a href='` + *metricPath + `?recovery=true'>Metrics with recovery</a></p>
                          </body>
                                </html>`)

  //configs Configs
  metricsExporter *Exporter
  handlers = map[string]http.Handler {}
)

// NewExporter returns a new Oracle DB exporter for the provided DSN.
func NewExporter() *Exporter {
  e := Exporter{
    	  duration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
    	    Namespace: namespace,
    	    Subsystem: exporter,
    	    Name:      "last_scrape_duration_seconds",
    	    Help:      "Duration of the last scrape of metrics from Oracle DB.",
    	  }, []string{}),
    	  totalScrapes: prometheus.NewCounterVec(prometheus.CounterOpts{
    	    Namespace: namespace,
    	    Subsystem: exporter,
    	    Name:      "scrapes_total",
    	    Help:      "Total number of times Oracle DB was scraped for metrics.",
    	  }, []string{"database","dbinstance"}),
    	  scrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
    	    Namespace: namespace,
    	    Subsystem: exporter,
    	    Name:      "scrape_errors_total",
    	    Help:      "Total number of times an error occured scraping a Oracle database.",
    	  }, []string{"database","dbinstance"}),
    	  error: prometheus.NewGaugeVec(prometheus.GaugeOpts{
    	    Namespace: namespace,
    	    Subsystem: exporter,
    	    Name:      "last_scrape_error",
    	    Help:      "Whether the last scrape of metrics from Oracle DB resulted in an error (1 for error, 0 for success).",
    	  },[]string{"database","dbinstance"}),
    	  sysmetric: prometheus.NewGaugeVec(prometheus.GaugeOpts{
    	    Namespace: namespace,
               Name:      "sysmetric",
               Help:      "Gauge metric with read/write pysical IOPs/bytes (v$sysmetric).",
          }, []string{"database", "dbinstance", "type"}),
          waitclass: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "waitclass",
               Help:      "Gauge metric with Waitevents (v$waitclassmetric).",
          }, []string{"database", "dbinstance", "type"}),
          sysstat: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "sysstat",
               Help:      "Gauge metric with commits/rollbacks/parses (v$sysstat).",
          }, []string{"database", "dbinstance", "type"}),
          session: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "session",
               Help:      "Gauge metric user/system active/passive sessions (v$session).",
          }, []string{"database", "dbinstance", "type", "state"}),
          uptime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "uptime",
               Help:      "Gauge metric with uptime in days of the Instance.",
          }, []string{"database", "dbinstance"}),
          tablespace: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "tablespace",
               Help:      "Gauge metric with total/free size of the Tablespaces.",
          }, []string{"database", "dbinstance", "type", "name", "contents", "autoextend"}),
          interconnect: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "interconnect",
               Help:      "Gauge metric with interconnect block transfers (v$sysstat).",
          }, []string{"database", "dbinstance", "type"}),
          recovery: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "recovery",
               Help:      "Gauge metric with percentage usage of FRA (v$recovery_file_dest).",
          }, []string{"database", "dbinstance", "type"}),
          redo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "redo",
               Help:      "Gauge metric with Redo log switches over last 5 min (v$log_history).",
          }, []string{"database", "dbinstance"}),
          cache: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "cachehitratio",
               Help:      "Gauge metric witch Cache hit ratios (v$sysmetric).",
          }, []string{"database", "dbinstance", "type"}),
          up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "up",
               Help:      "Whether the Oracle server is up.",
          }, []string{"database", "dbinstance"}),
          alertlog: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "error",
               Help:      "Oracle Errors occured during configured interval.",
          }, []string{"database", "dbinstance", "code", "description", "ignore"}),
          alertdate: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "error_unix_seconds",
               Help:      "Unixtime of Alertlog modified Date.",
          }, []string{"database", "dbinstance"}),
          services: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "services",
               Help:      "Active Oracle Services (v$active_services).",
          }, []string{"database", "dbinstance", "name"}),
          parameter: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "parameter",
               Help:      "oracle Configuration Parameters (v$parameter).",
          }, []string{"database", "dbinstance", "name"}),
          // query: prometheus.NewGaugeVec(prometheus.GaugeOpts{
          //      Namespace: namespace,
          //      Name:      "query",
          //      Help:      "Self defined Queries from Configuration File.",
          // }, []string{"database", "dbinstance", "name", "column", "row"}),
          asmspace: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "asmspace",
               Help:      "Gauge metric with total/free size of the ASM Diskgroups.",
          }, []string{"database", "dbinstance", "type", "name"}),
          tablerows: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "tablerows",
               Help:      "Gauge metric with rows of all Tables.",
          }, []string{"database", "dbinstance", "owner", "table_name", "tablespace"}),
          tablebytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "tablebytes",
               Help:      "Gauge metric with bytes of all Tables.",
          }, []string{"database", "dbinstance", "owner", "table_name"}),
          indexbytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "indexbytes",
               Help:      "Gauge metric with bytes of all Indexes per Table.",
          }, []string{"database", "dbinstance", "owner", "table_name"}),
          lobbytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
               Namespace: namespace,
               Name:      "lobbytes",
               Help:      "Gauge metric with bytes of all Lobs per Table.",
          }, []string{"database", "dbinstance", "owner", "table_name"}),
          custom: make(map[string]*prometheus.GaugeVec),
     }
     // add custom metrics
     for _, conn := range config.Cfgs {
          for _, query := range conn.Queries {
               log.Debug("Add Query " + query.Name)
               labels := []string{}
               for _, label := range query.Labels {
                    labels = append(labels, cleanName(label))
               }
               if  *pNoRownum == false {
                    e.custom[query.Name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
                         Namespace: namespace,
                         Name:      "custom_" + cleanName(query.Name),
                         Help:      query.Help,
                    },
                    append(labels, "metric", "database", "dbinstance", "rownum"))
               } else {
                    e.custom[query.Name] = prometheus.NewGaugeVec(prometheus.GaugeOpts{
                         Namespace: namespace,
                         Name:      "custom_" + cleanName(query.Name),
                         Help:      query.Help,
                    },
                    append(labels, "metric", "database", "dbinstance"))
                  }
           }
     }

     return &e
}

// ScrapeCustomQueries collects metrics from self defined queries from configuration file.
func (e *Exporter) ScrapeCustomQueries(pNoRownum bool) {
     var (
          rows *sql.Rows
          err  error
     )
     for _, config := range e.configs {
        db := config.db

         if db != nil {
               for _, query := range config.Queries {
                    log.Debug("execute "+ query.Name)
                    rows, err = db.Query(query.Sql)
                    if err != nil {
                         log.Error("Error in Query '" +  query.Sql + "': ")
                    fmt.Print(err)
                         continue
                    }

                    cols, _ := rows.Columns()
                    vals := make([]interface{}, len(cols))

                    defer rows.Close()
                    var rownum int = 1

                    for rows.Next() {
                         for i := range cols {
                              vals[i] = &vals[i]
                         }

                         err = rows.Scan(vals...)
                         if err != nil {
                              break
                         }

                    MetricLoop:
                         for _, metric := range query.Metrics {
                              metricColumnIndex := -1
                              for i, col := range cols {
                                   if cleanName(metric) == cleanName(col) {
                                        log.Debugln("Metric column '" + metric + "' found")
                                        metricColumnIndex = i
                                        break
                                   }
                              }

                              if metricColumnIndex == -1 {
                                   log.Errorln("Metric column '" + metric + "' not found")
                                   continue MetricLoop
                              }

                              if metricValue, ok := vals[metricColumnIndex].(float64); ok {
                                   promLabels := prometheus.Labels{}
                                   promLabels["database"] = config.Database
                                   promLabels["dbinstance"] = config.Instance
                                   promLabels["metric"] = metric
                                   if pNoRownum == false {
                                        promLabels["rownum"] = strconv.Itoa(rownum)
                                   }
                              LebelLoop:
                                   for _, label := range query.Labels {
                                        labelColumnIndex := -1
                                        for i, col := range cols {
                                             if cleanName(label) == cleanName(col) {
                                                  log.Debugln("Label column '" + label + "' found")
                                                  labelColumnIndex = i
                                                  break
                                             }
                                        }

                                        if labelColumnIndex == -1 {
                                             log.Errorln("Label column '" + label + "' not found")
                                             break LebelLoop
                                        }

                                        if a, ok := vals[labelColumnIndex].(string); ok {
                                             promLabels[cleanName(label)] = a
                                        } else if b, ok := vals[labelColumnIndex].(float64); ok {
                                             // if value is integer
                                             if b == float64(int64(b)) {
                                                  promLabels[cleanName(label)] = strconv.Itoa(int(b))
                                             } else {
                                                  promLabels[cleanName(label)] = strconv.FormatFloat(b, 'e', -1, 64)
                                             }
                                        }
                                   }
                                   e.custom[query.Name].With(promLabels).Set(metricValue)
                              }
                         }

                         rownum++
                    }
               }
		  }
	 }
}

// ScrapeQuery collects metrics from self defined queries from configuration file.
// func (e *Exporter) ScrapeQuery() {
//      var (
//           rows *sql.Rows
//           err  error
//      )
//      for _, conn := range config.Cfgs {
//           if conn.db != nil {
//                for _, query := range conn.Queries {
//                     rows, err = conn.db.Query(query.Sql)
//                     if err != nil {
//                          continue
//                     }

//                     cols, _ := rows.Columns()
//                     vals := make([]interface{}, len(cols))
//                     var rownum int = 1

//                     defer rows.Close()
//                     for rows.Next() {
//                          for i := range cols {
//                               vals[i] = &vals[i]
//                          }

//                          err = rows.Scan(vals...)
//                          if err != nil {
//                               break
//                          }

//                          for i := range cols {
//                               if value, ok := vals[i].(float64); ok {
//                                    e.query.WithLabelValues(conn.Database, conn.Instance, query.Name, cols[i], strconv.Itoa(rownum)).Set(value)
//                               }
//                          }
//                          rownum++
//                     }
//                }
//           }
//      }
// }

// ScrapeParameters collects metrics from the v$parameters view.
func (e *Exporter) ScrapeParameter() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	  db := config.db

    //num  metric_name
    //43  sessions
    if db != nil {
      rows, err = db.Query(`select name,value from v$parameter WHERE num=43`)
      if err != nil {
        fmt.Println(err)
        return
      }

      defer rows.Close()

      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name,&value); err != nil {
          break
        }
        name = cleanName(name)
        e.parameter.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }	
}

// ScrapeServices collects metrics from the v$active_services view.
func (e *Exporter) ScrapeServices() {
     var (
          rows *sql.Rows
    err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`select name from v$active_services`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        if err := rows.Scan(&name); err != nil {
          break
        }
        name = cleanName(name)
        e.services.WithLabelValues(config.Database,config.Instance,name).Set(1)
      }
	}
  }	
}

// ScrapeCache collects session metrics from the v$sysmetrics view.
func (e *Exporter) ScrapeCache() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    //metric_id  metric_name
    //2000    Buffer Cache Hit Ratio
    //2050    Cursor Cache Hit Ratio
    //2112    Library Cache Hit Ratio
    //2110    Row Cache Hit Ratio
  
    if db != nil {
      rows, err = db.Query(`select metric_name,value
                                 from v$sysmetric
                                 where group_id=2 and metric_id in (2000,2050,2112,2110)`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name, &value); err != nil {
          break
        }
        name = cleanName(name)
        e.cache.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }	
}

// ScrapeRecovery collects tablespace metrics
func (e *Exporter) ScrapeRedo() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`select count(*) from v$log_history where first_time > sysdate - 1/24/12`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var value float64
        if err := rows.Scan(&value); err != nil {
          break
        }
        e.redo.WithLabelValues(config.Database,config.Instance).Set(value)
      }
	}
  }
}

// ScrapeRecovery collects tablespace metrics
func (e *Exporter) ScrapeRecovery() {
  var (
    rows *sql.Rows
    err  error
  )

  for _, config := range e.configs {
	db := config.db

  if db != nil {
    rows, err = db.Query(`SELECT sum(percent_space_used) , sum(percent_space_reclaimable)
                             from V$FLASH_RECOVERY_AREA_USAGE`)
    if err != nil {
          fmt.Println(err)
          return
    }
    defer rows.Close()
    for rows.Next() {
      var used float64
      var recl float64
      if err := rows.Scan(&used, &recl); err != nil {
        break
      }
      e.recovery.WithLabelValues(config.Database,config.Instance,"percent_space_used").Set(used)
      e.recovery.WithLabelValues(config.Database,config.Instance,"percent_space_reclaimable").Set(recl)
    }
  }
 }
}

// ScrapeTablespaces collects tablespace metrics
func (e *Exporter) ScrapeInterconnect() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`SELECT name, value
                                 FROM V$SYSSTAT
                                 WHERE name in ('gc cr blocks served','gc cr blocks flushed','gc cr blocks received')`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name, &value); err != nil {
          break
        }
        name = cleanName(name)
        e.interconnect.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }	
}

// ScrapeAsmspace collects ASM metrics
func (e *Exporter) ScrapeAsmspace() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`SELECT g.name, sum(d.total_mb), sum(d.free_mb)
                                  FROM v$asm_disk d, v$asm_diskgroup g
                                 WHERE  d.group_number = g.group_number
                                  AND  d.header_status = 'MEMBER'
                                 GROUP by  g.name,  g.group_number`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var tsize float64
        var tfree float64
        if err := rows.Scan(&name, &tsize, &tfree); err != nil {
          break
        }
        e.asmspace.WithLabelValues(config.Database,config.Instance,"total",name).Set(tsize)
        e.asmspace.WithLabelValues(config.Database,config.Instance,"free",name).Set(tfree)
        e.asmspace.WithLabelValues(config.Database,config.Instance,"used",name).Set(tsize-tfree)
      }
	}
  }	
}

// ScrapeTablespaces collects tablespace metrics
func (e *Exporter) ScrapeTablespace() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`WITH
                                   getsize AS (SELECT tablespace_name, autoextensible, SUM(bytes) tsize
                                               FROM dba_data_files GROUP BY tablespace_name, autoextensible),
                                   getfree as (SELECT tablespace_name, contents, SUM(blocks*block_size) tfree
                                               FROM DBA_LMT_FREE_SPACE a, v$tablespace b, dba_tablespaces c
                                               WHERE a.TABLESPACE_ID= b.ts# and b.name=c.tablespace_name
                                               GROUP BY tablespace_name,contents)
                                 SELECT a.tablespace_name, b.contents, a.tsize,  b.tfree, a.autoextensible autoextend
                                 FROM GETSIZE a, GETFREE b
                                 WHERE a.tablespace_name = b.tablespace_name
                                 UNION
                                 SELECT tablespace_name, 'TEMPORARY', sum(tablespace_size), sum(free_space), 'NO'
                                 FROM dba_temp_free_space
                                 GROUP BY tablespace_name`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var contents string
        var tsize float64
        var tfree float64
        var auto string
        if err := rows.Scan(&name, &contents, &tsize, &tfree, &auto); err != nil {
          break
        }
        e.tablespace.WithLabelValues(config.Database,config.Instance,"total",name,contents,auto).Set(tsize)
        e.tablespace.WithLabelValues(config.Database,config.Instance,"free",name,contents,auto).Set(tfree)
        e.tablespace.WithLabelValues(config.Database,config.Instance,"used",name,contents,auto).Set(tsize-tfree)
      }
	}
  }	
}

// ScrapeSessions collects session metrics from the v$session view.
func (e *Exporter) ScrapeSession() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`SELECT decode(username,NULL,'SYSTEM','SYS','SYSTEM','USER'), status,count(*)
                                 FROM v$session
                                 GROUP BY decode(username,NULL,'SYSTEM','SYS','SYSTEM','USER'),status`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var user string
        var status string
        var value float64
        if err := rows.Scan(&user, &status, &value); err != nil {
            log.Errorln(fmt.Print(err))
          break
        }
        e.session.WithLabelValues(config.Database,config.Instance,user,status).Set(value)
      }
	}
  }
}

// ScrapeUptime Instance uptime
func (e *Exporter) ScrapeUptime() {
  var uptime float64

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err := db.Query("select sysdate-startup_time from v$instance")
      if err != nil {
            fmt.Println(err)
            return
      }
  
      defer rows.Close()
      rows.Next()
      err = rows.Scan(&uptime)
      if err == nil {
        e.uptime.WithLabelValues(config.Database,config.Instance).Set(uptime)
      } else {
            fmt.Println(err)
       }
	}
  }
}

// ScrapeSysstat collects activity metrics from the v$sysstat view.
func (e *Exporter) ScrapeSysstat() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`SELECT name, value FROM v$sysstat
                                      WHERE statistic# in (6,7,1084,1089)`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name, &value); err != nil {
          break
        }
        name = cleanName(name)
        e.sysstat.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }
}

// ScrapeWaitTime collects wait time metrics from the v$waitclassmetric view.
func (e *Exporter) ScrapeWaitclass() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    if db != nil {
      rows, err = db.Query(`SELECT n.wait_class, round(m.time_waited/m.INTSIZE_CSEC,3)
                                    FROM v$waitclassmetric  m, v$system_wait_class n
                                    WHERE m.wait_class_id=n.wait_class_id and n.wait_class != 'Idle'`)
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name, &value); err != nil {
          break
        }
        name = cleanName(name)
        e.waitclass.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }
}

// ScrapeSysmetrics collects session metrics from the v$sysmetrics view.
func (e *Exporter) ScrapeSysmetric() {
     var (
          rows *sql.Rows
          err  error
  )

  for _, config := range e.configs {
	db := config.db

    //metric_id  metric_name
    //2092    Physical Read Total IO Requests Per Sec
    //2093    Physical Read Total Bytes Per Sec
    //2100    Physical Write Total IO Requests Per Sec
    //2124    Physical Write Total Bytes Per Sec
    if db != nil {
      rows, err = db.Query("select metric_name,value from v$sysmetric where metric_id in (2092,2093,2124,2100)")
      if err != nil {
            fmt.Println(err)
            return
      }
      defer rows.Close()
      for rows.Next() {
        var name string
        var value float64
        if err := rows.Scan(&name, &value); err != nil {
          break
        }
        name = cleanName(name)
        e.sysmetric.WithLabelValues(config.Database,config.Instance,name).Set(value)
      }
	}
  }
}

// ScrapeTablerows collects bytes from dba_tables view.
func (e *Exporter) ScrapeTablerows() {
     var (
          rows *sql.Rows
          err  error
     )
	 for _, config := range e.configs {
		db := config.db
	    if db != nil {
           rows, err = db.Query(`select owner,table_name, tablespace_name, num_rows
                             from dba_tables
                             where owner not like '%SYS%' and num_rows is not null`)
           if err != nil {
                fmt.Println(err)
                return
           }
           defer rows.Close()
           for rows.Next() {
                var owner string
                var name string
                var space string
                var value float64
                if err := rows.Scan(&owner, &name, &space, &value); err != nil {
                     break
                }
                name = cleanName(name)
                e.tablerows.WithLabelValues(config.Database, config.Instance, owner, name, space).Set(value)
           }
		}
	}
}

func (e *Exporter) ScrapeTablebytes() {
     // ScrapeTablebytes collects bytes from dba_tables/dba_segments view.
     var (
          rows *sql.Rows
          err  error
     )
	 for _, config := range e.configs {
		db := config.db
	
        if db != nil {
             rows, err = db.Query(`SELECT tab.owner, tab.table_name,  stab.bytes
                               FROM dba_tables  tab, dba_segments stab
                               WHERE stab.owner = tab.owner AND stab.segment_name = tab.table_name
                               AND tab.owner NOT LIKE '%SYS%'`)
             if err != nil {
                  fmt.Println(err)
                  return
             }
             defer rows.Close()
             for rows.Next() {
                  var owner string
                  var name string
                  var value float64
                  if err = rows.Scan(&owner, &name, &value); err != nil {
                       break
                  }
                  name = cleanName(name)
                  e.tablebytes.WithLabelValues(config.Database, config.Instance, owner, name).Set(value)
             }
		}
	 }	
}

// ScrapeTablebytes collects bytes from dba_indexes/dba_segments view.
func (e *Exporter) ScrapeIndexbytes() {
     var (
          rows *sql.Rows
          err  error
     )
	 for _, config := range e.configs {
		db := config.db
		if db != nil {
           rows, err = db.Query(`select table_owner,table_name, sum(bytes)
                             from dba_indexes ind, dba_segments seg
                             WHERE ind.owner=seg.owner and ind.index_name=seg.segment_name
                             and table_owner NOT LIKE '%SYS%'
                             group by table_owner,table_name`)
           if err != nil {
                fmt.Println(err)
                return
           }
           defer rows.Close()
           for rows.Next() {
                var owner string
                var name string
                var value float64
                if err = rows.Scan(&owner, &name, &value); err != nil {
                     break
                }
                name = cleanName(name)
                e.indexbytes.WithLabelValues(config.Database, config.Instance, owner, name).Set(value)
           }
		}
	 }
}

// ScrapeLobbytes collects bytes from dba_lobs/dba_segments view.
func (e *Exporter) ScrapeLobbytes() {
     var (
          rows *sql.Rows
          err  error
     )
	 for _, config := range e.configs {
		db := config.db
          if db != nil {
               rows, err = db.Query(`select l.owner, l.table_name, sum(bytes)
                                 from dba_lobs l, dba_segments seg
                                 WHERE l.owner=seg.owner and l.table_name=seg.segment_name
                                 and l.owner NOT LIKE '%SYS%'
                                 group by l.owner,l.table_name`)
               if err != nil {
                    fmt.Println(err)
                    return
               }
               defer rows.Close()
               for rows.Next() {
                    var owner string
                    var name string
                    var value float64
                    if err = rows.Scan(&owner, &name, &value); err != nil {
                         break
                    }
                    name = cleanName(name)
                    e.lobbytes.WithLabelValues(config.Database, config.Instance, owner, name).Set(value)
               }
		  }  
   }
}

// Describe describes all the metrics exported by the Oracle exporter.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
     e.duration.Describe(ch)
     e.totalScrapes.Describe(ch)
     e.scrapeErrors.Describe(ch)
     e.session.Describe(ch)
     e.sysstat.Describe(ch)
     e.waitclass.Describe(ch)
     e.sysmetric.Describe(ch)
     e.interconnect.Describe(ch)
     e.tablespace.Describe(ch)
     e.recovery.Describe(ch)
     e.redo.Describe(ch)
     e.cache.Describe(ch)
     e.uptime.Describe(ch)
     e.up.Describe(ch)
     e.alertlog.Describe(ch)
     e.alertdate.Describe(ch)
     e.services.Describe(ch)
     e.parameter.Describe(ch)
     //e.query.Describe(ch)
     e.asmspace.Describe(ch)
     e.tablerows.Describe(ch)
     e.tablebytes.Describe(ch)
     e.indexbytes.Describe(ch)
     e.lobbytes.Describe(ch)
     for _, metric := range e.custom {
          metric.Describe(ch)
     }
}

// Connect the DBs and gather Databasename and Instancename
func (e *Exporter) Connect() {
     e.up.Reset()
     e.session.Reset()
     e.sysstat.Reset()
     e.waitclass.Reset()
     e.sysmetric.Reset()
     e.interconnect.Reset()
     e.tablespace.Reset()
     e.recovery.Reset()
     e.redo.Reset()
     e.cache.Reset()
     e.uptime.Reset()
     e.alertlog.Reset()
     e.alertdate.Reset()
     e.services.Reset()
     e.parameter.Reset()

     e.asmspace.Reset()
     e.tablerows.Reset()
     e.tablebytes.Reset()
     e.indexbytes.Reset()
     e.lobbytes.Reset()

     for _, metric := range e.custom {
          metric.Reset()
     }
    for i, config := range e.configs {

     log.Infoln(fmt.Sprintf("open dbConnection %d ", i) + "for "+ config.Database+"/"+config.Instance)
     db , err := sql.Open("oci8", config.Connection)
   
	 if err != nil {
		log.Error("db for config" + config.Database + "/"+ config.Instance + ":")
		fmt.Println(err)
		e.up.WithLabelValues(config.Database,config.Instance).Set(0)
		return
	 }
	 // test connection
	 if db != nil {
		rows, err := db.Query(`select 1 from dual`)
		if err != nil {
   		  log.Error("Db error for config " + config.Database + "/"+ config.Instance + ":")
		  fmt.Println(err)

		  e.up.WithLabelValues(config.Database,config.Instance).Set(0)
  
		  if db != nil {
			db.Close()
			config.db = nil
			 log.Infoln("closed dbConnection on error for "+ config.Database+"/"+config.Instance)
	  
		   }
		  continue
		}
  
		rows.Close()
  
	 }

     // only assigned working db connection
     config.db = db
	 // db is up:
	 e.up.WithLabelValues(config.Database,config.Instance).Set(1)

  }
}

// Close Connections
func (e *Exporter) Close() {
  for _, config := range e.configs {

	if config.db != nil {
       config.db.Close()
       log.Infoln("closed dbConnection on for "+ config.Database+"/"+config.Instance)
       config.db = nil
	}
  }
}

// Collect implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
  begun := time.Now()

  e.Connect()
  for _, config := range e.configs {
	e.totalScrapes.WithLabelValues(config.Database,config.Instance).Inc()
  }	
  defer e.Close()

	 e.up.Collect(ch)

     if e.vRecovery || *pRecovery {
          e.ScrapeRecovery()
          e.recovery.Collect(ch)
     }

     if *pMetrics {
          e.ScrapeUptime()
          e.uptime.Collect(ch)

          e.ScrapeSession()
          e.session.Collect(ch)

          e.ScrapeSysstat()
          e.sysstat.Collect(ch)

          e.ScrapeWaitclass()
          e.waitclass.Collect(ch)

          e.ScrapeSysmetric()
          e.sysmetric.Collect(ch)

          e.ScrapeTablespace()
          e.tablespace.Collect(ch)

          e.ScrapeInterconnect()
          e.interconnect.Collect(ch)

          e.ScrapeRedo()
          e.redo.Collect(ch)

          e.ScrapeCache()
          e.cache.Collect(ch)

          e.ScrapeAlertlog()
          e.alertlog.Collect(ch)
          e.alertdate.Collect(ch)

          e.ScrapeServices()
          e.services.Collect(ch)

          e.ScrapeParameter()
          e.parameter.Collect(ch)

          e.ScrapeAsmspace()
          e.asmspace.Collect(ch)
     }

     e.ScrapeCustomQueries(*pNoRownum)
     for _, metric := range e.custom {
          metric.Collect(ch)
     }
     //e.ScrapeQuery()
     //e.query.Collect(ch)

     if e.vTabRows || *pTabRows {
          e.ScrapeTablerows()
          e.tablerows.Collect(ch)
     }

     if e.vTabBytes || *pTabBytes {
          e.ScrapeTablebytes()
          e.tablebytes.Collect(ch)
     }

     if e.vIndBytes || *pIndBytes {
          e.ScrapeIndexbytes()
          e.indexbytes.Collect(ch)
     }

     if e.vLobBytes || *pLobBytes {
          e.ScrapeLobbytes()
          e.lobbytes.Collect(ch)
     }

     e.duration.WithLabelValues().Set(time.Since(begun).Seconds())
	 e.duration.Collect(ch)
	 e.totalScrapes.Collect(ch)
	 e.error.Collect(ch)
	 e.scrapeErrors.Collect(ch)

}

func (e *Exporter) Handler(w http.ResponseWriter, r *http.Request) {
  promhttp.Handler().ServeHTTP(w, r)
}


//func createKeyValuePairs(m map[string]string) string {
//    b := new(bytes.Buffer)
//    for key, value := range m {
//        fmt.Fprintf(b, "%s=\"%s\"\n", key, value)
//    }
//    return b.String()
//}

func ScrapeHandler(w http.ResponseWriter, r *http.Request) {
  

  target := r.URL.Query().Get("target")
  target_plusopts := r.URL.String()

  log.Infoln("ScrapeHandler for " + target_plusopts)

  if handlers[target_plusopts] != nil {
  	log.Infoln("resuse Exporter" + target_plusopts)
  } else {
    registry := prometheus.NewRegistry()
    e := NewExporter()
	e.vTabRows = false
	e.vTabBytes = false
	e.vIndBytes = false
	e.vLobBytes = false
	e.vRecovery = false
	if r.URL.Query().Get("tablerows") == "true" {
		 e.vTabRows = true
	}
	if r.URL.Query().Get("tablebytes") == "true" {
		 e.vTabBytes = true
	}
	if r.URL.Query().Get("indexbytes") == "true" {
		 e.vIndBytes = true
	}
	if r.URL.Query().Get("lobbytes") == "true" {
		 e.vLobBytes = true
	}
	if r.URL.Query().Get("recovery") == "true" {
		 e.vRecovery = true
	}
  
	c:=[]*Config{}
	
    for i, conn := range config.Cfgs {
       log.Infoln("check Database" + conn.Database+ " vs " + target)
       if target == "" || conn.Database == target {
		  log.Infoln("add Database" + conn.Database)
		  // Note: reference orig list element, as conn reference is updated
		  cp :=config.Cfgs[i]
		  c = append(c,&cp)
        }
     }
	e.configs = c

	e.lastIp = ""
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		 e.lastIp = ip
	}
	registry.MustRegister(e)
	handlers[target_plusopts] = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

  }

  // Delegate http serving to Prometheus client library, which will call collector.Collect.
  h := handlers[target_plusopts]

  if h == nil {
    http.Error(w, fmt.Sprintf("Target not found %v", target), 400)
    return
  } 

  h.ServeHTTP(w, r)
}


func main() {
     flag.Parse()

     manageService()
     
     log.Infoln("Starting Prometheus Oracle exporter " + Version)
    //metricsExporter = NewExporter()
     if loadConfig() {
          log.Infoln("Config loaded: ", *configFile)
          //exporter := NewExporter()
          //prometheus.MustRegister(exporter)

          http.HandleFunc(*metricPath, ScrapeHandler)
          //http.HandleFunc("/telemetrie", exporter.Handler)

          http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write(landingPage) })

          log.Infoln("Listening on", *listenAddress)
          log.Fatal(http.ListenAndServe(*listenAddress, nil))
     }
}
