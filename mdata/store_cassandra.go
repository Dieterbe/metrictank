package mdata

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gocql/gocql"
	"github.com/grafana/metrictank/cassandra"
	"github.com/grafana/metrictank/mdata/chunk"
	"github.com/grafana/metrictank/stats"
	"github.com/grafana/metrictank/tracing"
	"github.com/hailocab/go-hostpool"
	opentracing "github.com/opentracing/opentracing-go"
	tags "github.com/opentracing/opentracing-go/ext"
	"github.com/raintank/worldping-api/pkg/log"
)

// write aggregated data to cassandra.

const Month_sec = 60 * 60 * 24 * 28

const keyspace_schema = `CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}  AND durable_writes = true`
const table_schema = `CREATE TABLE IF NOT EXISTS %s.%s (
    key ascii,
    ts int,
    data blob,
    PRIMARY KEY (key, ts)
) WITH CLUSTERING ORDER BY (ts DESC)
    AND compaction = { 'class': 'TimeWindowCompactionStrategy', 'compaction_window_unit': 'HOURS', 'compaction_window_size': '%d', 'tombstone_threshold': '0.2', 'tombstone_compaction_interval': '86400'}
    AND compression = { 'class': 'LZ4Compressor' }
    AND gc_grace_seconds = %d`
const Table_name_format = `metric_%d`

var (
	errChunkTooSmall  = errors.New("unpossibly small chunk in cassandra")
	errStartBeforeEnd = errors.New("start must be before end.")
	errReadQueueFull  = errors.New("the read queue is full")
	errReadTooOld     = errors.New("the read is too old")
	errTableNotFound  = errors.New("table for given TTL not found")

	// metric store.cassandra.get.exec is the duration of getting from cassandra store
	cassGetExecDuration = stats.NewLatencyHistogram15s32("store.cassandra.get.exec")
	// metric store.cassandra.get.wait is the duration of the get spent in the queue
	cassGetWaitDuration = stats.NewLatencyHistogram12h32("store.cassandra.get.wait")
	// metric store.cassandra.put.exec is the duration of putting in cassandra store
	cassPutExecDuration = stats.NewLatencyHistogram15s32("store.cassandra.put.exec")
	// metric store.cassandra.put.wait is the duration of a put in the wait queue
	cassPutWaitDuration = stats.NewLatencyHistogram12h32("store.cassandra.put.wait")
	// reads that were already too old to be executed
	cassOmitOldRead = stats.NewCounter32("store.cassandra.omit_read.too_old")
	// reads that could not be pushed into the queue because it was full
	cassReadQueueFull = stats.NewCounter32("store.cassandra.omit_read.queue_full")

	// metric store.cassandra.chunks_per_row is how many chunks are retrieved per row in get queries
	cassChunksPerRow = stats.NewMeter32("store.cassandra.chunks_per_row", false)
	// metric store.cassandra.rows_per_response is how many rows come per get response
	cassRowsPerResponse = stats.NewMeter32("store.cassandra.rows_per_response", false)
	// metric store.cassandra.get_chunks is the duration of how long it takes to get chunks
	cassGetChunksDuration = stats.NewLatencyHistogram15s32("store.cassandra.get_chunks")
	// metric store.cassandra.to_iter is the duration of converting chunks to iterators
	cassToIterDuration = stats.NewLatencyHistogram15s32("store.cassandra.to_iter")

	// metric store.cassandra.chunk_operations.save_ok is counter of successful saves
	chunkSaveOk = stats.NewCounter32("store.cassandra.chunk_operations.save_ok")
	// metric store.cassandra.chunk_operations.save_fail is counter of failed saves
	chunkSaveFail = stats.NewCounter32("store.cassandra.chunk_operations.save_fail")
	// metric store.cassandra.chunk_size.at_save is the sizes of chunks seen when saving them
	chunkSizeAtSave = stats.NewMeter32("store.cassandra.chunk_size.at_save", true)
	// metric store.cassandra.chunk_size.at_load is the sizes of chunks seen when loading them
	chunkSizeAtLoad = stats.NewMeter32("store.cassandra.chunk_size.at_load", true)

	errmetrics = cassandra.NewErrMetrics("store.cassandra")
)

/*
https://godoc.org/github.com/gocql/gocql#Session
Session is the interface used by users to interact with the database.
It's safe for concurrent use by multiple goroutines and a typical usage scenario is to have one global session
object to interact with the whole Cassandra cluster.
*/

type TTLTables map[uint32]ttlTable
type ttlTable struct {
	Table      string
	WindowSize uint32
}

type CassandraStore struct {
	Session          *gocql.Session
	writeQueues      []chan *ChunkWriteRequest
	writeQueueMeters []*stats.Range32
	readQueue        chan *ChunkReadRequest
	ttlTables        TTLTables
	omitReadTimeout  time.Duration
	tracer           opentracing.Tracer
}

func ttlUnits(ttl uint32) float64 {
	// convert ttl to hours
	return float64(ttl) / (60 * 60)
}

func PrepareChunkData(span uint32, data []byte) []byte {
	chunkSizeAtSave.Value(len(data))
	version := chunk.FormatStandardGoTszWithSpan
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, version)

	spanCode, ok := chunk.RevChunkSpans[span]
	if !ok {
		// it's probably better to panic than to persist the chunk with a wrong length
		panic(fmt.Sprintf("Chunk span invalid: %d", span))
	}
	binary.Write(buf, binary.LittleEndian, spanCode)
	buf.Write(data)
	return buf.Bytes()
}

func GetTTLTables(ttls []uint32, windowFactor int, nameFormat string) TTLTables {
	tables := make(TTLTables)
	for _, ttl := range ttls {
		tables[ttl] = GetTTLTable(ttl, windowFactor, nameFormat)
	}
	return tables
}

func GetTTLTable(ttl uint32, windowFactor int, nameFormat string) ttlTable {
	/*
	 * the purpose of this is to bucket metrics of similar TTLs.
	 * we first calculate the largest power of 2 that's smaller than the TTL and then divide the result by
	 * the window factor. for example with a window factor of 20 we want to group the metrics like this:
	 *
	 * generated with: https://gist.github.com/replay/69ad7cfd523edfa552cd12851fa74c58
	 *
	 * +------------------------+---------------+---------------------+----------+
	 * |              TTL hours |    table_name | window_size (hours) | sstables |
	 * +------------------------+---------------+---------------------+----------+
	 * |         0 <= hours < 1 |     metrics_0 |                   1 |    0 - 2 |
	 * |         1 <= hours < 2 |     metrics_1 |                   1 |    1 - 3 |
	 * |         2 <= hours < 4 |     metrics_2 |                   1 |    2 - 5 |
	 * |         4 <= hours < 8 |     metrics_4 |                   1 |    4 - 9 |
	 * |        8 <= hours < 16 |     metrics_8 |                   1 |   8 - 17 |
	 * |       16 <= hours < 32 |    metrics_16 |                   1 |  16 - 33 |
	 * |       32 <= hours < 64 |    metrics_32 |                   2 |  16 - 33 |
	 * |      64 <= hours < 128 |    metrics_64 |                   4 |  16 - 33 |
	 * |     128 <= hours < 256 |   metrics_128 |                   7 |  19 - 38 |
	 * |     256 <= hours < 512 |   metrics_256 |                  13 |  20 - 41 |
	 * |    512 <= hours < 1024 |   metrics_512 |                  26 |  20 - 41 |
	 * |   1024 <= hours < 2048 |  metrics_1024 |                  52 |  20 - 41 |
	 * |   2048 <= hours < 4096 |  metrics_2048 |                 103 |  20 - 41 |
	 * |   4096 <= hours < 8192 |  metrics_4096 |                 205 |  20 - 41 |
	 * |  8192 <= hours < 16384 |  metrics_8192 |                 410 |  20 - 41 |
	 * | 16384 <= hours < 32768 | metrics_16384 |                 820 |  20 - 41 |
	 * | 32768 <= hours < 65536 | metrics_32768 |                1639 |  20 - 41 |
	 * +------------------------+---------------+---------------------+----------+
	 */

	// calculate the pre factor window by finding the largest power of 2 that's smaller than ttl
	preFactorWindow := uint32(math.Exp2(math.Floor(math.Log2(ttlUnits(ttl)))))
	tableName := fmt.Sprintf(nameFormat, preFactorWindow)
	return ttlTable{
		Table:      tableName,
		WindowSize: preFactorWindow/uint32(windowFactor) + 1,
	}
}

func NewCassandraStore(addrs, keyspace, consistency, CaPath, Username, Password, hostSelectionPolicy string, timeout, readers, writers, readqsize, writeqsize, retries, protoVer, windowFactor, omitReadTimeout int, ssl, auth, hostVerification bool, createKeyspace bool, ttls []uint32) (*CassandraStore, error) {

	stats.NewGauge32("store.cassandra.write_queue.size").Set(writeqsize)
	stats.NewGauge32("store.cassandra.num_writers").Set(writers)

	cluster := gocql.NewCluster(strings.Split(addrs, ",")...)
	if ssl {
		cluster.SslOpts = &gocql.SslOptions{
			CaPath:                 CaPath,
			EnableHostVerification: hostVerification,
		}
	}
	if auth {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: Username,
			Password: Password,
		}
	}
	cluster.Consistency = gocql.ParseConsistency(consistency)
	cluster.Timeout = time.Duration(timeout) * time.Millisecond
	cluster.NumConns = writers
	cluster.ProtoVersion = protoVer
	var err error
	tmpSession, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}

	ttlTables := GetTTLTables(ttls, windowFactor, Table_name_format)

	// create or verify the metrictank keyspace
	if createKeyspace {
		err = tmpSession.Query(fmt.Sprintf(keyspace_schema, keyspace)).Exec()
		if err != nil {
			return nil, err
		}
		for _, result := range ttlTables {
			err := tmpSession.Query(fmt.Sprintf(table_schema, keyspace, result.Table, result.WindowSize, result.WindowSize*60*60)).Exec()
			if err != nil {
				return nil, err
			}
		}

		if err != nil {
			return nil, err
		}
	} else {
		var keyspaceMetadata *gocql.KeyspaceMetadata
		// five attempts to verify the keyspace exists before returning an error
	AttemptLoop:
		for attempt := 1; attempt > 0; attempt++ {
			keyspaceMetadata, err = tmpSession.KeyspaceMetadata(keyspace)
			if err != nil {
				log.Warn("cassandra keyspace not found; attempt: %v", attempt)
				if attempt >= 5 {
					return nil, err
				}
				time.Sleep(5 * time.Second)
			} else {
				for _, result := range ttlTables {
					if _, ok := keyspaceMetadata.Tables[result.Table]; !ok {
						log.Warn("cassandra table %s not found; attempt: %v", result.Table, attempt)
						if attempt >= 5 {
							return nil, err
						}
						time.Sleep(5 * time.Second)
						continue AttemptLoop
					}
				}
				break
			}
		}
	}

	tmpSession.Close()
	cluster.Keyspace = keyspace
	cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: retries}

	switch hostSelectionPolicy {
	case "roundrobin":
		cluster.PoolConfig.HostSelectionPolicy = gocql.RoundRobinHostPolicy()
	case "hostpool-simple":
		cluster.PoolConfig.HostSelectionPolicy = gocql.HostPoolHostPolicy(hostpool.New(nil))
	case "hostpool-epsilon-greedy":
		cluster.PoolConfig.HostSelectionPolicy = gocql.HostPoolHostPolicy(
			hostpool.NewEpsilonGreedy(nil, 0, &hostpool.LinearEpsilonValueCalculator{}),
		)
	case "tokenaware,roundrobin":
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(
			gocql.RoundRobinHostPolicy(),
		)
	case "tokenaware,hostpool-simple":
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(
			gocql.HostPoolHostPolicy(hostpool.New(nil)),
		)
	case "tokenaware,hostpool-epsilon-greedy":
		cluster.PoolConfig.HostSelectionPolicy = gocql.TokenAwareHostPolicy(
			gocql.HostPoolHostPolicy(
				hostpool.NewEpsilonGreedy(nil, 0, &hostpool.LinearEpsilonValueCalculator{}),
			),
		)
	default:
		return nil, fmt.Errorf("unknown HostSelectionPolicy '%q'", hostSelectionPolicy)
	}

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, err
	}
	log.Debug("CS: created session to %s keysp %s cons %v with policy %s timeout %d readers %d writers %d readq %d writeq %d retries %d proto %d ssl %t auth %t hostverif %t", addrs, keyspace, consistency, hostSelectionPolicy, timeout, readers, writers, readqsize, writeqsize, retries, protoVer, ssl, auth, hostVerification)
	c := &CassandraStore{
		Session:          session,
		writeQueues:      make([]chan *ChunkWriteRequest, writers),
		writeQueueMeters: make([]*stats.Range32, writers),
		readQueue:        make(chan *ChunkReadRequest, readqsize),
		omitReadTimeout:  time.Duration(omitReadTimeout) * time.Second,
		ttlTables:        ttlTables,
		tracer:           opentracing.NoopTracer{},
	}

	for i := 0; i < writers; i++ {
		c.writeQueues[i] = make(chan *ChunkWriteRequest, writeqsize)
		c.writeQueueMeters[i] = stats.NewRange32(fmt.Sprintf("store.cassandra.write_queue.%d.items", i+1))
		go c.processWriteQueue(c.writeQueues[i], c.writeQueueMeters[i])
	}

	for i := 0; i < readers; i++ {
		go c.processReadQueue()
	}

	return c, err
}

func (c *CassandraStore) SetTracer(t opentracing.Tracer) {
	c.tracer = t
}

func (c *CassandraStore) Add(cwr *ChunkWriteRequest) {
	sum := 0
	for _, char := range cwr.key {
		sum += int(char)
	}
	which := sum % len(c.writeQueues)
	c.writeQueueMeters[which].Value(len(c.writeQueues[which]))
	c.writeQueues[which] <- cwr
}

/* process writeQueue.
 */
func (c *CassandraStore) processWriteQueue(queue chan *ChunkWriteRequest, meter *stats.Range32) {
	tick := time.Tick(time.Duration(1) * time.Second)
	for {
		select {
		case <-tick:
			meter.Value(len(queue))
		case cwr := <-queue:
			meter.Value(len(queue))
			log.Debug("CS: starting to save %s:%d %v", cwr.key, cwr.chunk.T0, cwr.chunk)
			//log how long the chunk waited in the queue before we attempted to save to cassandra
			cassPutWaitDuration.Value(time.Now().Sub(cwr.timestamp))

			buf := PrepareChunkData(cwr.span, cwr.chunk.Series.Bytes())
			success := false
			attempts := 0
			for !success {
				err := c.insertChunk(cwr.key, cwr.chunk.T0, cwr.ttl, buf)

				if err == nil {
					success = true
					cwr.metric.SyncChunkSaveState(cwr.chunk.T0)
					SendPersistMessage(cwr.key, cwr.chunk.T0)
					log.Debug("CS: save complete. %s:%d %v", cwr.key, cwr.chunk.T0, cwr.chunk)
					chunkSaveOk.Inc()
				} else {
					errmetrics.Inc(err)
					if (attempts % 20) == 0 {
						log.Warn("CS: failed to save chunk to cassandra after %d attempts. %v, %s", attempts+1, cwr.chunk, err)
					}
					chunkSaveFail.Inc()
					sleepTime := 100 * attempts
					if sleepTime > 2000 {
						sleepTime = 2000
					}
					time.Sleep(time.Duration(sleepTime) * time.Millisecond)
					attempts++
				}
			}
		}
	}
}

func (c *CassandraStore) GetTableNames() []string {
	names := make([]string, 0)
	for _, table := range c.ttlTables {
		names = append(names, table.Table)
	}
	return names
}

func (c *CassandraStore) getTable(ttl uint32) (string, error) {
	entry, ok := c.ttlTables[ttl]
	if !ok {
		return "", errTableNotFound
	}
	return entry.Table, nil
}

// Insert Chunks into Cassandra.
//
// key: is the metric_id
// ts: is the start of the aggregated time range.
// data: is the payload as bytes.
func (c *CassandraStore) insertChunk(key string, t0, ttl uint32, data []byte) error {
	// for unit tests
	if c.Session == nil {
		return nil
	}

	table, err := c.getTable(ttl)
	if err != nil {
		return err
	}

	query := fmt.Sprintf("INSERT INTO %s (key, ts, data) values(?,?,?) USING TTL %d", table, ttl)
	row_key := fmt.Sprintf("%s_%d", key, t0/Month_sec) // "month number" based on unix timestamp (rounded down)
	pre := time.Now()
	ret := c.Session.Query(query, row_key, t0, data).Exec()
	cassPutExecDuration.Value(time.Now().Sub(pre))
	return ret
}

type outcome struct {
	month   uint32
	sortKey uint32
	i       *gocql.Iter
	omitted bool
}
type asc []outcome

func (o asc) Len() int           { return len(o) }
func (o asc) Swap(i, j int)      { o[i], o[j] = o[j], o[i] }
func (o asc) Less(i, j int) bool { return o[i].sortKey < o[j].sortKey }

func (c *CassandraStore) processReadQueue() {
	for crr := range c.readQueue {
		waitDuration := time.Since(crr.timestamp)
		cassGetWaitDuration.Value(waitDuration)
		if waitDuration > c.omitReadTimeout {
			cassOmitOldRead.Inc()
			crr.out <- outcome{omitted: true}
			continue
		}
		pre := time.Now()
		iter := outcome{crr.month, crr.sortKey, c.Session.Query(crr.q, crr.p...).Iter(), false}
		cassGetExecDuration.Value(time.Since(pre))
		crr.out <- iter
	}
}

// Basic search of cassandra in the table for given ttl
// start inclusive, end exclusive
func (c *CassandraStore) Search(ctx context.Context, key string, ttl, start, end uint32) ([]chunk.IterGen, error) {
	table, err := c.getTable(ttl)
	if err != nil {
		return nil, err
	}
	return c.SearchTable(ctx, key, table, start, end)
}

// Basic search of cassandra in given table
// start inclusive, end exclusive
func (c *CassandraStore) SearchTable(ctx context.Context, key, table string, start, end uint32) ([]chunk.IterGen, error) {
	_, span := tracing.NewSpan(ctx, c.tracer, "CassandraStore.SearchTable")
	defer span.Finish()
	tags.SpanKindRPCClient.Set(span)
	tags.PeerService.Set(span, "cassandra")

	itgens := make([]chunk.IterGen, 0)
	if start > end {
		tracing.Failure(span)
		tracing.Error(span, errStartBeforeEnd)
		return itgens, errStartBeforeEnd
	}

	pre := time.Now()

	crrs := make([]*ChunkReadRequest, 0)

	query := func(month, sortKey uint32, q string, p ...interface{}) {
		crrs = append(crrs, &ChunkReadRequest{month, sortKey, q, p, pre, nil})
	}

	start_month := start - (start % Month_sec)       // starting row has to be at, or before, requested start
	end_month := (end - 1) - ((end - 1) % Month_sec) // ending row has to include the last point we might need (end-1)

	// unfortunately in the database we only have the t0's of all chunks.
	// this means we can easily make sure to include the correct last chunk (just query for a t0 < end, the last chunk will contain the last needed data)
	// but it becomes hard to find which should be the first chunk to include. we can't just query for start <= t0 because than will miss some data at the beginning
	// we can't assume we know the chunkSpan so we can't just calculate the t0 >= start - <some-predefined-number> because chunkSpans may change over time.
	// we effectively need all chunks with a t0 > start, as well as the last chunk with a t0 <= start.
	// since we make sure that you can only use chunkSpans so that Month_sec % chunkSpan == 0, we know that this previous chunk will always be in the same row
	// as the one that has start_month.

	row_key := fmt.Sprintf("%s_%d", key, start_month/Month_sec)

	query(start_month, start_month, fmt.Sprintf("SELECT ts, data FROM %s WHERE key=? AND ts <= ? Limit 1", table), row_key, start)

	if start_month == end_month {
		// we need a selection of the row between startTs and endTs
		row_key = fmt.Sprintf("%s_%d", key, start_month/Month_sec)
		query(start_month, start_month+1, fmt.Sprintf("SELECT ts, data FROM %s WHERE key = ? AND ts > ? AND ts < ? ORDER BY ts ASC", table), row_key, start, end)
	} else {
		// get row_keys for each row we need to query.
		for month := start_month; month <= end_month; month += Month_sec {
			row_key = fmt.Sprintf("%s_%d", key, month/Month_sec)
			if month == start_month {
				// we want from startTs to the end of the row.
				query(month, month+1, fmt.Sprintf("SELECT ts, data FROM %s WHERE key = ? AND ts >= ? ORDER BY ts ASC", table), row_key, start+1)
			} else if month == end_month {
				// we want from start of the row till the endTs.
				query(month, month, fmt.Sprintf("SELECT ts, data FROM %s WHERE key = ? AND ts <= ? ORDER BY ts ASC", table), row_key, end-1)
			} else {
				// we want all columns
				query(month, month, fmt.Sprintf("SELECT ts, data FROM %s WHERE key = ? ORDER BY ts ASC", table), row_key)
			}
		}
	}
	numQueries := len(crrs)
	results := make(chan outcome, numQueries)
	for i := range crrs {
		crrs[i].out = results
		select {
		case c.readQueue <- crrs[i]:
		default:
			cassReadQueueFull.Inc()
			tracing.Failure(span)
			tracing.Error(span, errReadQueueFull)
			return nil, errReadQueueFull
		}
	}
	outcomes := make([]outcome, 0, numQueries)

	seen := 0
	for o := range results {
		if o.omitted {
			tracing.Failure(span)
			tracing.Error(span, errReadTooOld)
			return nil, errReadTooOld
		}
		seen += 1
		outcomes = append(outcomes, o)
		if seen == numQueries {
			close(results)
			break
		}
	}
	cassGetChunksDuration.Value(time.Since(pre))
	pre = time.Now()
	// we have all of the results, but they could have arrived in any order.
	sort.Sort(asc(outcomes))

	var b []byte
	var ts int
	for _, outcome := range outcomes {
		chunks := int64(0)
		for outcome.i.Scan(&ts, &b) {
			chunks += 1
			chunkSizeAtLoad.Value(len(b))
			if len(b) < 2 {
				tracing.Failure(span)
				tracing.Error(span, errChunkTooSmall)
				return itgens, errChunkTooSmall
			}
			itgen, err := chunk.NewGen(b, uint32(ts))
			if err != nil {
				tracing.Failure(span)
				tracing.Error(span, err)
				return itgens, err
			}
			itgens = append(itgens, *itgen)
		}
		err := outcome.i.Close()
		if err != nil {
			tracing.Failure(span)
			tracing.Error(span, err)
			errmetrics.Inc(err)
		} else {
			cassChunksPerRow.Value(int(chunks))
		}
	}
	cassToIterDuration.Value(time.Now().Sub(pre))
	cassRowsPerResponse.Value(len(outcomes))
	span.SetTag("outcomes", len(outcomes))
	span.SetTag("itgens", len(itgens))
	return itgens, nil
}

func (c *CassandraStore) Stop() {
	c.Session.Close()
}
