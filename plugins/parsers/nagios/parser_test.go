package nagios

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/metric"
	"github.com/influxdata/telegraf/testutil"
)

func TestGetExitCode(t *testing.T) {
	tests := []struct {
		name    string
		errF    func() error
		expCode int
		expErr  error
	}{
		{
			name: "nil error passed is ok",
			errF: func() error {
				return nil
			},
			expCode: 0,
			expErr:  nil,
		},
		{
			name: "unexpected error type",
			errF: func() error {
				return errors.New("not *exec.ExitError")
			},
			expCode: 3,
			expErr:  errors.New("not *exec.ExitError"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.errF()
			code, err := getExitCode(e)

			require.Equal(t, tt.expCode, code)
			require.Equal(t, tt.expErr, err)
		})
	}
}

type metricBuilder struct {
	name      string
	tags      map[string]string
	fields    map[string]interface{}
	timestamp time.Time
}

func mb() *metricBuilder {
	return &metricBuilder{}
}

func (b *metricBuilder) n(v string) *metricBuilder {
	b.name = v
	return b
}

func (b *metricBuilder) f(k string, v interface{}) *metricBuilder {
	if b.fields == nil {
		b.fields = make(map[string]interface{})
	}
	b.fields[k] = v
	return b
}

func (b *metricBuilder) b() telegraf.Metric {
	m := metric.New(b.name, b.tags, b.fields, b.timestamp)
	return m
}

// assertEqual asserts two slices to be equal. Note, that the order
// of the entries matters.
func assertEqual(t *testing.T, exp, actual []telegraf.Metric) {
	require.Len(t, actual, len(exp))
	for i := 0; i < len(exp); i++ {
		ok := testutil.MetricEqual(exp[i], actual[i])
		require.True(t, ok)
	}
}

func TestTryAddState(t *testing.T) {
	tests := []struct {
		name          string
		runErrF       func() error
		runErrMessage []byte
		metrics       []telegraf.Metric
		assertF       func(*testing.T, []telegraf.Metric)
	}{
		{
			name: "should append state=0 field to existing metric",
			runErrF: func() error {
				return nil
			},
			metrics: []telegraf.Metric{
				mb().
					n("nagios").
					f("perfdata", 0).b(),
				mb().
					n("nagios_state").
					f("service_output", "OK: system working").b(),
			},
			assertF: func(t *testing.T, metrics []telegraf.Metric) {
				exp := []telegraf.Metric{
					mb().
						n("nagios").
						f("perfdata", 0).b(),
					mb().
						n("nagios_state").
						f("service_output", "OK: system working").
						f("state", 0).b(),
				}
				assertEqual(t, exp, metrics)
			},
		},
		{
			name: "should create 'nagios_state state=0' and same timestamp as others",
			runErrF: func() error {
				return nil
			},
			metrics: []telegraf.Metric{
				mb().
					n("nagios").
					f("perfdata", 0).b(),
			},
			assertF: func(t *testing.T, metrics []telegraf.Metric) {
				exp := []telegraf.Metric{
					mb().
						n("nagios").
						f("perfdata", 0).b(),
					mb().
						n("nagios_state").
						f("state", 0).b(),
				}
				assertEqual(t, exp, metrics)
			},
		},
		{
			name: "should create 'nagios_state state=0' and recent timestamp",
			runErrF: func() error {
				return nil
			},
			metrics: make([]telegraf.Metric, 0),
			assertF: func(t *testing.T, metrics []telegraf.Metric) {
				require.Len(t, metrics, 1)
				m := metrics[0]
				require.Equal(t, "nagios_state", m.Name())
				s, ok := m.GetField("state")
				require.True(t, ok)
				require.Equal(t, int64(0), s)
				require.WithinDuration(t, time.Now().UTC(), m.Time(), 10*time.Second)
			},
		},
		{
			name: "should return metrics with state unknown and thrown error is service_output",
			runErrF: func() error {
				return errors.New("non parsable error")
			},
			metrics: []telegraf.Metric{
				mb().
					n("nagios_state").b(),
			},
			assertF: func(t *testing.T, metrics []telegraf.Metric) {
				exp := []telegraf.Metric{
					mb().
						n("nagios_state").
						f("state", 3).
						f("service_output", "non parsable error").b(),
				}

				assertEqual(t, exp, metrics)
			},
		},
		{
			name: "should return metrics with state unknown and service_output error from error message parameter",
			runErrF: func() error {
				return errors.New("")
			},
			runErrMessage: []byte("some error message"),
			metrics: []telegraf.Metric{
				mb().
					n("nagios_state").b(),
			},
			assertF: func(t *testing.T, metrics []telegraf.Metric) {
				exp := []telegraf.Metric{
					mb().
						n("nagios_state").
						f("state", 3).
						f("service_output", "some error message").b(),
				}
				assertEqual(t, exp, metrics)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := AddState(tt.runErrF(), tt.runErrMessage, tt.metrics)
			tt.assertF(t, metrics)
		})
	}
}

func assertNagiosState(t *testing.T, m telegraf.Metric, f map[string]interface{}) {
	require.Equal(t, map[string]string{}, m.Tags())
	require.Equal(t, f, m.Fields())
}

func TestParse(t *testing.T) {
	parser := Parser{
		metricName: "nagios_test",
	}

	tests := []struct {
		name    string
		input   string
		assertF func(*testing.T, []telegraf.Metric, error)
	}{
		{
			name: "valid output 1",
			input: `PING OK - Packet loss = 0%, RTA = 0.30 ms|rta=0.298000ms;4000.000000;6000.000000;0.000000 pl=0%;80;90;0;100
This is a long output
with three lines
`,
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 3)
				// rta
				require.Equal(t, map[string]string{
					"unit":     "ms",
					"perfdata": "rta",
				}, metrics[0].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(0.298),
					"warning_lt":  float64(0),
					"warning_gt":  float64(4000),
					"critical_lt": float64(0),
					"critical_gt": float64(6000),
					"min":         float64(0),
				}, metrics[0].Fields())

				// pl
				require.Equal(t, map[string]string{
					"unit":     "%",
					"perfdata": "pl",
				}, metrics[1].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(0),
					"warning_lt":  float64(0),
					"warning_gt":  float64(80),
					"critical_lt": float64(0),
					"critical_gt": float64(90),
					"min":         float64(0),
					"max":         float64(100),
				}, metrics[1].Fields())

				assertNagiosState(t, metrics[2], map[string]interface{}{
					"service_output":      "PING OK - Packet loss = 0%, RTA = 0.30 ms",
					"long_service_output": "This is a long output\nwith three lines",
				})
			},
		},
		{
			name:  "valid output 2",
			input: "TCP OK - 0.008 second response time on port 80|time=0.008457s;;;0.000000;10.000000",
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 2)
				// time
				require.Equal(t, map[string]string{
					"unit":     "s",
					"perfdata": "time",
				}, metrics[0].Tags())
				require.Equal(t, map[string]interface{}{
					"value": float64(0.008457),
					"min":   float64(0),
					"max":   float64(10),
				}, metrics[0].Fields())

				assertNagiosState(t, metrics[1], map[string]interface{}{
					"service_output": "TCP OK - 0.008 second response time on port 80",
				})
			},
		},
		{
			name:  "valid output 3",
			input: "TCP OK - 0.008 second response time on port 80|time=0.008457",
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 2)
				// time
				require.Equal(t, map[string]string{
					"perfdata": "time",
				}, metrics[0].Tags())
				require.Equal(t, map[string]interface{}{
					"value": float64(0.008457),
				}, metrics[0].Fields())

				assertNagiosState(t, metrics[1], map[string]interface{}{
					"service_output": "TCP OK - 0.008 second response time on port 80",
				})
			},
		},
		{
			name:  "valid output 4",
			input: "OK: Load average: 0.00, 0.01, 0.05 | 'load1'=0.00;~:4;@0:6;0; 'load5'=0.01;3;0:5;0; 'load15'=0.05;0:2;0:4;0;",
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 4)
				// load1
				require.Equal(t, map[string]string{
					"perfdata": "load1",
				}, metrics[0].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(0.00),
					"warning_lt":  MinFloat64,
					"warning_gt":  float64(4),
					"critical_le": float64(0),
					"critical_ge": float64(6),
					"min":         float64(0),
				}, metrics[0].Fields())

				// load5
				require.Equal(t, map[string]string{
					"perfdata": "load5",
				}, metrics[1].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(0.01),
					"warning_gt":  float64(3),
					"warning_lt":  float64(0),
					"critical_lt": float64(0),
					"critical_gt": float64(5),
					"min":         float64(0),
				}, metrics[1].Fields())

				// load15
				require.Equal(t, map[string]string{
					"perfdata": "load15",
				}, metrics[2].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(0.05),
					"warning_lt":  float64(0),
					"warning_gt":  float64(2),
					"critical_lt": float64(0),
					"critical_gt": float64(4),
					"min":         float64(0),
				}, metrics[2].Fields())

				assertNagiosState(t, metrics[3], map[string]interface{}{
					"service_output": "OK: Load average: 0.00, 0.01, 0.05",
				})
			},
		},
		{
			name:  "no perf data",
			input: "PING OK - Packet loss = 0%, RTA = 0.30 ms",
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 1)

				assertNagiosState(t, metrics[0], map[string]interface{}{
					"service_output": "PING OK - Packet loss = 0%, RTA = 0.30 ms",
				})
			},
		},
		{
			name:  "malformed perf data",
			input: "PING OK - Packet loss = 0%, RTA = 0.30 ms| =3;;;; dgasdg =;;;; sff=;;;;",
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 1)

				assertNagiosState(t, metrics[0], map[string]interface{}{
					"service_output": "PING OK - Packet loss = 0%, RTA = 0.30 ms",
				})
			},
		},
		{
			name: "from https://assets.nagios.com/downloads/nagioscore/docs/nagioscore/3/en/pluginapi.html",
			input: `DISK OK - free space: / 3326 MB (56%); | /=2643MB;5948;5958;0;5968
/ 15272 MB (77%);
/boot 68 MB (69%);
/home 69357 MB (27%);
/var/log 819 MB (84%); | /boot=68MB;88;93;0;98
/home=69357MB;253404;253409;0;253414
/var/log=818MB;970;975;0;980
`,
			assertF: func(t *testing.T, metrics []telegraf.Metric, err error) {
				require.NoError(t, err)
				require.Len(t, metrics, 5)
				// /=2643MB;5948;5958;0;5968
				require.Equal(t, map[string]string{
					"unit":     "MB",
					"perfdata": "/",
				}, metrics[0].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(2643),
					"warning_lt":  float64(0),
					"warning_gt":  float64(5948),
					"critical_lt": float64(0),
					"critical_gt": float64(5958),
					"min":         float64(0),
					"max":         float64(5968),
				}, metrics[0].Fields())

				// /boot=68MB;88;93;0;98
				require.Equal(t, map[string]string{
					"unit":     "MB",
					"perfdata": "/boot",
				}, metrics[1].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(68),
					"warning_lt":  float64(0),
					"warning_gt":  float64(88),
					"critical_lt": float64(0),
					"critical_gt": float64(93),
					"min":         float64(0),
					"max":         float64(98),
				}, metrics[1].Fields())

				// /home=69357MB;253404;253409;0;253414
				require.Equal(t, map[string]string{
					"unit":     "MB",
					"perfdata": "/home",
				}, metrics[2].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(69357),
					"warning_lt":  float64(0),
					"warning_gt":  float64(253404),
					"critical_lt": float64(0),
					"critical_gt": float64(253409),
					"min":         float64(0),
					"max":         float64(253414),
				}, metrics[2].Fields())

				// /var/log=818MB;970;975;0;980
				require.Equal(t, map[string]string{
					"unit":     "MB",
					"perfdata": "/var/log",
				}, metrics[3].Tags())
				require.Equal(t, map[string]interface{}{
					"value":       float64(818),
					"warning_lt":  float64(0),
					"warning_gt":  float64(970),
					"critical_lt": float64(0),
					"critical_gt": float64(975),
					"min":         float64(0),
					"max":         float64(980),
				}, metrics[3].Fields())

				assertNagiosState(t, metrics[4], map[string]interface{}{
					"service_output":      "DISK OK - free space: / 3326 MB (56%);",
					"long_service_output": "/ 15272 MB (77%);\n/boot 68 MB (69%);\n/home 69357 MB (27%);\n/var/log 819 MB (84%);",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics, err := parser.Parse([]byte(tt.input))
			tt.assertF(t, metrics, err)
		})
	}
}

func TestParseThreshold(t *testing.T) {
	tests := []struct {
		input string
		eMin  float64
		eMax  float64
		eErr  error
	}{
		{
			input: "10",
			eMin:  0,
			eMax:  10,
			eErr:  nil,
		},
		{
			input: "10:",
			eMin:  10,
			eMax:  MaxFloat64,
			eErr:  nil,
		},
		{
			input: "~:10",
			eMin:  MinFloat64,
			eMax:  10,
			eErr:  nil,
		},
		{
			input: "10:20",
			eMin:  10,
			eMax:  20,
			eErr:  nil,
		},
		{
			input: "10:20",
			eMin:  10,
			eMax:  20,
			eErr:  nil,
		},
		{
			input: "10:20:30",
			eMin:  0,
			eMax:  0,
			eErr:  ErrBadThresholdFormat,
		},
	}

	for i := range tests {
		vmin, vmax, err := parseThreshold(tests[i].input)
		require.InDelta(t, tests[i].eMin, vmin, testutil.DefaultDelta)
		require.InDelta(t, tests[i].eMax, vmax, testutil.DefaultDelta)
		require.Equal(t, tests[i].eErr, err)
	}
}

const benchmarkData = `DISK OK - free space: / 3326 MB (56%); | /=2643MB;5948;5958;0;5968
/ 15272 MB (77%);
/boot 68 MB (69%);
`

func TestBenchmarkData(t *testing.T) {
	plugin := &Parser{}

	expected := []telegraf.Metric{
		metric.New(
			"nagios",
			map[string]string{
				"perfdata": "/",
				"unit":     "MB",
			},
			map[string]interface{}{
				"critical_gt": 5958.0,
				"critical_lt": 0.0,
				"min":         0.0,
				"max":         5968.0,
				"value":       2643.0,
				"warning_gt":  5948.0,
				"warning_lt":  0.0,
			},
			time.Unix(0, 0),
		),
		metric.New(
			"nagios_state",
			map[string]string{},
			map[string]interface{}{
				"long_service_output": "/ 15272 MB (77%);\n/boot 68 MB (69%);",
				"service_output":      "DISK OK - free space: / 3326 MB (56%);",
			},
			time.Unix(0, 0),
		),
	}

	actual, err := plugin.Parse([]byte(benchmarkData))
	require.NoError(t, err)
	testutil.RequireMetricsEqual(t, expected, actual, testutil.IgnoreTime(), testutil.SortMetrics())
}

func BenchmarkParsing(b *testing.B) {
	plugin := &Parser{}

	for n := 0; n < b.N; n++ {
		//nolint:errcheck // Benchmarking so skip the error check to avoid the unnecessary operations
		plugin.Parse([]byte(benchmarkData))
	}
}
