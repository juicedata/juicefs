// Copyright 2015 Databricks
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

val scaleFactor = "5"

// data format.
val format = "parquet"
// If false, float type will be used instead of decimal.
val useDecimal = true
// If false, string type will be used instead of date.
val useDate = true
// If true, rows with nulls in partition key will be thrown away.
val filterNull = false
// If true, partitions will be coalesced into a single file during generation.
val shuffle = true

// s3/dbfs path to generate the data to.
val rootDir = s"jfs:///tmp/performance-datasets/tpcds/sf$scaleFactor-$format/"
// name of database to be created.
val databaseName = s"tpcds_sf${scaleFactor}" +
  s"""_${if (useDecimal) "with" else "no"}decimal""" +
  s"""_${if (useDate) "with" else "no"}date""" +
  s"""_${if (filterNull) "no" else "with"}nulls"""


import com.databricks.spark.sql.perf.tpcds.TPCDSTables
val sqlContext = new org.apache.spark.sql.SQLContext(sc)
val tables = new TPCDSTables(sqlContext, dsdgenDir = "/tmp/tpcds-kit/tools", scaleFactor = scaleFactor, useDoubleForDecimal = !useDecimal, useStringForDate = !useDate)


import org.apache.spark.deploy.SparkHadoopUtil
// Limit the memory used by parquet writer
// Compress with snappy:
sqlContext.sparkContext.hadoopConfiguration.set("parquet.memory.pool.ratio", "0.1")
sqlContext.setConf("spark.sql.parquet.compression.codec", "snappy")
// TPCDS has around 2000 dates.
spark.conf.set("spark.sql.shuffle.partitions", "10")
// Don't write too huge files.
sqlContext.setConf("spark.sql.files.maxRecordsPerFile", "20000000")

val dsdgen_partitioned=10 // recommended for SF10000+.
val dsdgen_nonpartitioned=10 // small tables do not need much parallelism in generation.

// generate all the small dimension tables
val nonPartitionedTables = Array("call_center", "catalog_page", "customer", "customer_address", "customer_demographics", "date_dim", "household_demographics", "income_band", "item", "promotion", "reason", "ship_mode", "store",  "time_dim", "warehouse", "web_page", "web_site")
nonPartitionedTables.foreach { t => {
  tables.genData(
      location = rootDir,
      format = format,
      overwrite = true,
      partitionTables = true,
      clusterByPartitionColumns = shuffle,
      filterOutNullPartitionValues = filterNull,
      tableFilter = t,
      numPartitions = dsdgen_nonpartitioned)
}}
println("Done generating non partitioned tables.")

// leave the biggest/potentially hardest tables to be generated last.
val partitionedTables = Array("inventory", "web_returns", "catalog_returns", "store_returns", "web_sales", "catalog_sales", "store_sales") 
partitionedTables.foreach { t => {
  tables.genData(
      location = rootDir,
      format = format,
      overwrite = true,
      partitionTables = true,
      clusterByPartitionColumns = shuffle,
      filterOutNullPartitionValues = filterNull,
      tableFilter = t,
      numPartitions = dsdgen_partitioned)
}}
println("Done generating partitioned tables.")

// COMMAND ----------

sql(s"drop database if exists $databaseName cascade")
sql(s"create database $databaseName")

// COMMAND ----------

sql(s"use $databaseName")

// COMMAND ----------

tables.createExternalTables(rootDir, format, databaseName, overwrite = true, discoverPartitions = true)

// COMMAND ----------

// MAGIC %md
// MAGIC Analyzing tables is needed only if cbo is to be used.

// COMMAND ----------

tables.analyzeTables(databaseName, analyzeColumns = true)

System.exit(0)
