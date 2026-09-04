#!/usr/bin/env node

import { existsSync } from "node:fs";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import path from "node:path";
import readline from "node:readline";

import {
  ZVecCollectionSchema,
  ZVecCreateAndOpen,
  ZVecDataType,
  ZVecIndexType,
  ZVecMetricType,
  ZVecOpen,
} from "@zvec/zvec";
import {
  createEmbeddingModel,
  EmbeddingPurpose,
} from "@zvec/zvec-grep";

const EMBEDDING_MODEL = "local/potion-code-16m-v2";
const SCHEMA_VERSION = 1;

class ZvecWorker {
  static async create(config) {
    const model = createEmbeddingModel(EMBEDDING_MODEL, {
      modelCacheDir: config.model_cache_path,
    });
    await model.embed([{ kind: "text", text: "maccy" }], {
      purpose: EmbeddingPurpose.Query,
    });
    const worker = new ZvecWorker(config, model);
    worker.collection = await worker.openCollection();
    return worker;
  }

  constructor(config, model) {
    this.collectionPath = config.collection_path;
    this.ftsTokenizer = config.fts_tokenizer;
    this.rankConstant = config.rrf_rank_constant;
    this.model = model;
    this.collection = null;
    this.metadata = {
      schema_version: SCHEMA_VERSION,
      embedding_model: model.info.reference,
      embedding_dimension: model.info.dimension,
      embedding_metric: model.info.metric,
      fts_tokenizer: this.ftsTokenizer,
    };
  }

  get metadataPath() {
    return `${this.collectionPath}.maccy.json`;
  }

  async openCollection() {
    if (existsSync(this.collectionPath)) {
      await this.validateMetadata();
      return ZVecOpen(this.collectionPath);
    }

    await mkdir(path.dirname(this.collectionPath), { recursive: true });
    const schema = new ZVecCollectionSchema({
      name: "maccy_clipboard_entries",
      fields: [{
        name: "plain_text",
        dataType: ZVecDataType.STRING,
        nullable: false,
        indexParams: {
          indexType: ZVecIndexType.FTS,
          tokenizerName: this.ftsTokenizer,
          filters: ["lowercase"],
        },
      }],
      vectors: [{
        name: "embedding",
        dataType: ZVecDataType.VECTOR_FP32,
        dimension: this.model.info.dimension,
        indexParams: {
          indexType: ZVecIndexType.HNSW,
          metricType: ZVecMetricType.COSINE,
        },
      }],
    });
    const collection = ZVecCreateAndOpen(this.collectionPath, schema);
    await this.writeMetadata();
    return collection;
  }

  async validateMetadata() {
    let actual;
    try {
      actual = JSON.parse(await readFile(this.metadataPath, "utf8"));
    } catch (error) {
      if (error?.code === "ENOENT") {
        throw new Error(
          `Zvec metadata is missing at ${this.metadataPath}; rebuild the collection`,
        );
      }
      throw error;
    }
    if (JSON.stringify(actual) !== JSON.stringify(this.metadata)) {
      throw new Error(
        "Zvec collection configuration changed; rebuild the collection before startup",
      );
    }
  }

  async writeMetadata() {
    const temporary = `${this.metadataPath}.tmp-${process.pid}`;
    await writeFile(temporary, JSON.stringify(this.metadata), "utf8");
    await rename(temporary, this.metadataPath);
  }

  async embedTexts(texts, purpose) {
    const vectors = [];
    const batchSize = this.model.info.limits.maxBatchSize;
    for (let start = 0; start < texts.length; start += batchSize) {
      const contents = texts.slice(start, start + batchSize).map((text) => ({
        kind: "text",
        text,
      }));
      const result = await this.model.embed(contents, { purpose });
      vectors.push(...result.vectors);
    }
    return vectors;
  }

  async upsert(entries) {
    if (entries.length === 0) return;
    const existing = this.collection.fetchSync({
      ids: entries.map((entry) => entry.id),
      outputFields: [],
      includeVector: false,
    });
    const missing = entries.filter((entry) => !(entry.id in existing));
    if (missing.length === 0) return;

    const vectors = await this.embedTexts(
      missing.map((entry) => entry.plain_text),
      EmbeddingPurpose.Document,
    );
    const statuses = this.collection.upsertSync(missing.map((entry, index) => ({
      id: entry.id,
      fields: { plain_text: entry.plain_text },
      vectors: { embedding: vectors[index] },
    })));
    const failure = statuses.find((status) => !status.ok);
    if (failure) throw new Error(failure.message || failure.code);
  }

  async search(query, limit) {
    const [vector] = await this.embedTexts([query], EmbeddingPurpose.Query);
    const candidateCount = Math.max(limit, 10);
    return this.collection.multiQuerySync({
      queries: [
        {
          fieldName: "plain_text",
          fts: { matchString: query },
          numCandidates: candidateCount,
        },
        {
          fieldName: "embedding",
          vector,
          numCandidates: candidateCount,
        },
      ],
      topk: limit,
      outputFields: [],
      rerank: { type: "rrf", rankConstant: this.rankConstant },
    }).map((doc) => ({ id: doc.id, score: doc.score }));
  }

  async close() {
    this.collection?.closeSync();
    await this.model.dispose();
  }
}

function reply(payload) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

async function serve() {
  const lines = readline.createInterface({ input: process.stdin });
  let worker = null;
  for await (const line of lines) {
    try {
      const request = JSON.parse(line);
      switch (request.command) {
        case "init":
          if (worker) throw new Error("worker is already initialized");
          worker = await ZvecWorker.create(request);
          reply({ ok: true });
          break;
        case "ping":
          if (!worker) throw new Error("worker is not initialized");
          reply({ ok: true });
          break;
        case "upsert":
          if (!worker) throw new Error("worker is not initialized");
          await worker.upsert(request.entries ?? []);
          reply({ ok: true });
          break;
        case "search":
          if (!worker) throw new Error("worker is not initialized");
          if (!request.query || !Number.isInteger(request.limit) || request.limit < 1) {
            throw new Error("search requires a non-empty query and positive limit");
          }
          reply({ ok: true, results: await worker.search(request.query, request.limit) });
          break;
        case "close":
          await worker?.close();
          return;
        default:
          throw new Error("unknown worker command");
      }
    } catch (error) {
      reply({ ok: false, error: String(error?.message ?? error).slice(0, 1000) });
    }
  }
  await worker?.close();
}

await serve();
