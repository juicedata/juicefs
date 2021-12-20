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

// Databricks notebook source
// MAGIC %md 
// MAGIC This notebook runs spark-sql-perf TPCDS benchmark on and saves the result.

// COMMAND ----------

// Database to be used:
// TPCDS Scale factor
val scaleFactor = "5"
// If false, float type will be used instead of decimal.
val useDecimal = true
// If false, string type will be used instead of date.
val useDate = true
// name of database to be used.
val filterNull = false

val databaseName = s"tpcds_sf${scaleFactor}" +
  s"""_${if (useDecimal) "with" else "no"}decimal""" +
  s"""_${if (useDate) "with" else "no"}date""" +
  s"""_${if (filterNull) "no" else "with"}nulls"""

val iterations = 2 // how many times to run the whole set of queries.

val timeout = 60 // timeout in hours

val query_filter = Seq("q1-v2.4", "q2-v2.4", "q3-v2.4", "q4-v2.4", "q5-v2.4", "q6-v2.4", "q7-v2.4", "q8-v2.4", "q9-v2.4", "q10-v2.4") // Seq() == all queries
val randomizeQueries = false // run queries in a random order. Recommended for parallel runs.

// detailed results will be written as JSON to this location.
val resultLocation = "file:///tmp/performance-datasets/tpcds/results"

// COMMAND ----------

// Spark configuration
spark.conf.set("spark.sql.broadcastTimeout", "10000") // good idea for Q14, Q88.

// ... + any other configuration tuning

// COMMAND ----------

sql(s"use `$databaseName`")

// COMMAND ----------

import com.databricks.spark.sql.perf.tpcds.TPCDS
val sqlContext = new org.apache.spark.sql.SQLContext(sc)
val tpcds = new TPCDS (sqlContext = sqlContext)
def queries = {
  val filtered_queries = query_filter match {
    case Seq() => tpcds.tpcds2_4Queries
    case _ => tpcds.tpcds2_4Queries.filter(q => query_filter.contains(q.name))
  }
  if (randomizeQueries) scala.util.Random.shuffle(filtered_queries) else filtered_queries
}
val experiment = tpcds.runExperiment(
  queries,
  iterations = iterations,
  resultLocation = resultLocation,
  tags = Map("runtype" -> "benchmark", "database" -> databaseName, "scale_factor" -> scaleFactor))

experiment.waitForFinish(timeout*60*60)

experiment.getCurrentResults.createOrReplaceTempView("result")
spark.sql("select substring(name,1,100) as Name, bround((parsingTime+analysisTime+optimizationTime+planningTime+executionTime)/1000.0,1) as Runtime_sec  from result").show()

System.exit(0)
//display(summary)
