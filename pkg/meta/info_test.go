/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package meta

import "testing"

func TestOlderThan(t *testing.T) {
	v := redisVersion{"2.2.10", 2, 2}
	if !v.olderThan(redisVersion{"6.2", 6, 2}) {
		t.Fatal("Expect true, got false.")
	}
	if !v.olderThan(redisVersion{"2.3", 2, 3}) {
		t.Fatal("Expect true, got false.")
	}
	if v.olderThan(redisVersion{"2.2", 2, 2}) {
		t.Fatal("Expect false, got true.")
	}
	if v.olderThan(redisVersion{"2.1", 2, 1}) {
		t.Fatal("Expect false, got true.")
	}
	if v.olderThan(v) {
		t.Fatal("Expect false, got true.")
	}
	if v.olderThan(redisVersion{}) {
		t.Fatal("Expect false, got true.")
	}
}

func TestParseRedisVersion(t *testing.T) {
	t.Run("Should return error for invalid redisVersion", func(t *testing.T) {
		invalidVers := []string{"", "2.sadf.1", "3", "t.3.4"}
		for _, v := range invalidVers {
			_, err := parseRedisVersion(v)
			if err == nil {
				t.Fail()
			}
		}
	})
	t.Run("Should parse redisVersion", func(t *testing.T) {
		ver, err := parseRedisVersion("6.2.19")
		if err != nil {
			t.Fatalf("Failed to parse a valid redisVersion: %s", err)
		}
		if !(ver.major == 6 && ver.minor == 2) {
			t.Fatalf("Expect %s, got %s", "6.2", ver)
		}
		if ver.String() != "6.2.19" {
			t.Fatalf("Expect %s, got %s", "6.2.19", ver)
		}
	})
}

func TestParseRedisInfo(t *testing.T) {
	t.Run("Should parse the fields we are interested in", func(t *testing.T) {
		input := `# Server
	redis_version:6.1.240
	redis_git_sha1:00000000
	redis_git_dirty:0
	redis_build_id:a26db646ea64a07c
	redis_mode:standalone
	os:Linux 5.4.0-1017-aws x86_64
	arch_bits:64
	multiplexing_api:epoll
	atomicvar_api:c11-builtin
	gcc_version:9.3.0
	process_id:2755423
	process_supervised:no
	run_id:d04b36ea49704b152d8ce82bf563d26bcd52e741
	tcp_port:6379
	server_time_usec:1610404734862725
	uptime_in_seconds:2430194
	uptime_in_days:28
	hz:10
	configured_hz:10
	lru_clock:16569214
	executable:/usr/local/bin/redis-server
	config_file:/etc/redis/redis.conf
	io_threads_active:0

		# Clients
	connected_clients:2
	cluster_connections:0
	maxclients:10000
	client_recent_max_input_buffer:24
	client_recent_max_output_buffer:0
	blocked_clients:0
	tracking_clients:0
	clients_in_timeout_table:0

		# Memory
	used_memory:200001664
	used_memory_human:190.74M
	used_memory_rss:210456576
	used_memory_rss_human:200.71M
	used_memory_peak:200060312
	used_memory_peak_human:190.79M
	used_memory_peak_perc:99.97%
		used_memory_overhead:54246680
	used_memory_startup:803648
	used_memory_dataset:145754984
	used_memory_dataset_perc:73.17%
		allocator_allocated:199994624
	allocator_active:200847360
	allocator_resident:209551360
	total_system_memory:16596942848
	total_system_memory_human:15.46G
	used_memory_lua:37888
	used_memory_lua_human:37.00K
	used_memory_scripts:0
	used_memory_scripts_human:0B
	number_of_cached_scripts:0
	maxmemory:200000000
	maxmemory_human:190.73M
	maxmemory_policy:allkeys-lru
	allocator_frag_ratio:1.00
	allocator_frag_bytes:852736
	allocator_rss_ratio:1.04
	allocator_rss_bytes:8704000
	rss_overhead_ratio:1.00
	rss_overhead_bytes:905216
	mem_fragmentation_ratio:1.05
	mem_fragmentation_bytes:10538760
	mem_not_counted_for_evict:0
	mem_replication_backlog:0
	mem_clients_slaves:0
	mem_clients_normal:41008
	mem_aof_buffer:0
	mem_allocator:jemalloc-5.1.0
	active_defrag_running:0
	lazyfree_pending_objects:0
	lazyfreed_objects:0

		# Persistence
	loading:0
	rdb_changes_since_last_save:6407091
	rdb_bgsave_in_progress:0
	rdb_last_save_time:1607974540
	rdb_last_bgsave_status:ok
	rdb_last_bgsave_time_sec:-1
	rdb_current_bgsave_time_sec:-1
	rdb_last_cow_size:0
	aof_enabled:0
	aof_rewrite_in_progress:0
	aof_rewrite_scheduled:0
	aof_last_rewrite_time_sec:-1
	aof_current_rewrite_time_sec:-1
	aof_last_bgrewrite_status:ok
	aof_last_write_status:ok
	aof_last_cow_size:0
	module_fork_in_progress:0
	module_fork_last_cow_size:0

		# Stats
	total_connections_received:127469
	total_commands_processed:15725530
	instantaneous_ops_per_sec:8
	total_net_input_bytes:1305500885
	total_net_output_bytes:237264322
	instantaneous_input_kbps:0.74
	instantaneous_output_kbps:0.10
	rejected_connections:0
	sync_full:0
	sync_partial_ok:0
	sync_partial_err:0
	expired_keys:41809
	expired_stale_perc:0.00
	expired_time_cap_reached_count:0
	expire_cycle_cpu_milliseconds:75107
	evicted_keys:182417
	keyspace_hits:3627925
	keyspace_misses:1661042
	pubsub_channels:0
	pubsub_patterns:0
	latest_fork_usec:0
	total_forks:0
	migrate_cached_sockets:0
	slave_expires_tracked_keys:0
	active_defrag_hits:0
	active_defrag_misses:0
	active_defrag_key_hits:0
	active_defrag_key_misses:0
	tracking_total_keys:0
	tracking_total_items:0
	tracking_total_prefixes:0
	unexpected_error_replies:0
	dump_payload_sanitizations:0
	total_reads_processed:15835400
	total_writes_processed:15835323
	io_threaded_reads_processed:0
	io_threaded_writes_processed:0

		# Replication
	role:master
	connected_slaves:0
	master_replid:d4fc9b96fa0c5d3eb4c4444a394ba6e4e40cc0d5
	master_replid2:0000000000000000000000000000000000000000
	master_repl_offset:0
	second_repl_offset:-1
	repl_backlog_active:0
	repl_backlog_size:1048576
	repl_backlog_first_byte_offset:0
	repl_backlog_histlen:0

		# CPU
	used_cpu_sys:3574.527853
	used_cpu_user:13274.227145
	used_cpu_sys_children:0.000000
	used_cpu_user_children:0.000000
	used_cpu_sys_main_thread:3553.579738
	used_cpu_user_main_thread:13249.100447

		# Modules

		# Cluster
	cluster_enabled:0

		# Keyspace
	db0:keys=1125326,expires=5,avg_ttl=321749445601195`
		info, err := checkRedisInfo(input)
		if err != nil {
			t.Fatalf("Failed to parse redis info: %s", err)
		}
		if info.redisVersion != "6.1.240" {
			t.Fatalf("Expect %s, got %q", "6.1.240", info.redisVersion)
		}
		if info.aofEnabled {
			t.Fatalf("Expect %t, got %t", false, true)
		}
		if info.clusterEnabled {
			t.Fatalf("Expect %t, got %t", false, true)
		}
		if info.maxMemoryPolicy != "allkeys-lru" {
			t.Fatalf("Expect %s, got %s", "allkeys-lru", info.maxMemoryPolicy)
		}
	})
	t.Run("Test fields that may emit warnings", func(t *testing.T) {
		input := `# Server
	redis_version:2.1.0

		# Cluster
	cluster_enabled:1`
		info, err := checkRedisInfo(input)
		if err != nil {
			t.Fatalf("Failed to parse redis info: %s", err)
		}
		if !info.clusterEnabled {
			t.Fatalf("Expect %t, got %t", true, false)
		}
	})
}
