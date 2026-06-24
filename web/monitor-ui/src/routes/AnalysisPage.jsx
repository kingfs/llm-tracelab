import React, { useState } from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "../components/common/EmptyState";
import { DetailMetaPill, InlineTag } from "../components/common/Badges";
import { useJSON } from "../hooks/useJSON";
import { apiPaths, apiURL, postJSON } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatDateTime } from "../lib/monitor";

export function AnalysisPage() {
  const { t } = useI18n();
  const [refreshTick, setRefreshTick] = useState(0);
  const [batchBusy, setBatchBusy] = useState(false);
  const [jobNotice, setJobNotice] = useState(null);
  const analysis = useJSON(apiURL(apiPaths.analysis, { limit: "50" }), [refreshTick]);
  const jobs = useJSON(apiURL(apiPaths.analysisJobs, { limit: "50" }), [refreshTick]);
  const items = analysis.data?.items || [];
  const jobItems = jobs.data?.items || [];

  const runMissingUsageBatch = async () => {
    setBatchBusy(true);
    setJobNotice(null);
    try {
      const response = await postJSON(apiPaths.analysisBatchReanalyze, { mode: "async", missing_usage: true, limit: 1000, repair_usage: true });
      setJobNotice({ tone: "green", text: `Batch reanalysis job #${response.job?.id || "-"} ${response.job?.status || "queued"}` });
      setRefreshTick((value) => value + 1);
    } catch (error) {
      setJobNotice({ tone: "danger", text: error.message || "request failed" });
    } finally {
      setBatchBusy(false);
    }
  };

  const runUnparsedBatch = async () => {
    setBatchBusy(true);
    setJobNotice(null);
    try {
      const response = await postJSON(apiPaths.analysisBatchReanalyze, { mode: "async", observation: "unparsed", limit: 1000, reparse: true, scan: true });
      setJobNotice({ tone: "green", text: `Batch reanalysis job #${response.job?.id || "-"} ${response.job?.status || "queued"}` });
      setRefreshTick((value) => value + 1);
    } catch (error) {
      setJobNotice({ tone: "danger", text: error.message || "request failed" });
    } finally {
      setBatchBusy(false);
    }
  };

  return (
    <div className="shell shell-list">
      <header className="topbar">
        <div>
          <p className="eyebrow">Offline runs</p>
          <h1>{t("analysis.title")}</h1>
        </div>
        <div className="topbar-meta">
          <button className="ghost-button active" type="button" disabled={batchBusy} onClick={runUnparsedBatch}>
            {batchBusy ? t("analysis.queueing") : t("analysis.reparseUnparsed")}
          </button>
          <button className="ghost-button active" type="button" disabled={batchBusy} onClick={runMissingUsageBatch}>
            {batchBusy ? t("analysis.queueing") : t("analysis.repairMissingUsage")}
          </button>
        </div>
      </header>
      {jobNotice ? <EmptyState title={t("analysis.jobNotice")} detail={jobNotice.text} tone={jobNotice.tone} compact /> : null}
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Reanalysis jobs</p>
            <h2>{t("analysis.jobQueue")}</h2>
          </div>
          <InlineTag>{t("analysis.jobs", { count: jobs.data?.total ?? 0 })}</InlineTag>
        </div>
        {jobs.error ? <EmptyState title={t("analysis.loadJobsError")} detail={jobs.error} tone="danger" /> : null}
        {jobs.loading && !jobs.data ? <EmptyState title={t("analysis.loadingJobs")} detail={t("analysis.loadingJobsDetail")} /> : null}
        {jobItems.length ? (
          <div className="finding-list">
            {jobItems.map((job) => (
              <article key={job.id} className="finding-card">
                <div className="finding-card-head">
                  <div>
                    <strong>{job.job_type}</strong>
                    <span>{job.target_type} / {job.target_id}</span>
                  </div>
                  <InlineTag tone={job.status === "completed" ? "green" : job.status === "failed" ? "danger" : "gold"}>{job.status}</InlineTag>
                </div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="job" value={job.id} />
                  <DetailMetaPill label="attempts" value={job.attempts ?? 0} />
                  <DetailMetaPill label="created" value={formatDateTime(job.created_at)} />
                  <DetailMetaPill label="updated" value={formatDateTime(job.updated_at)} />
                </div>
                <pre className="code-block">{JSON.stringify({ steps: job.steps || [], request: job.request || {}, result: job.result || {}, error: job.last_error || "" }, null, 2)}</pre>
              </article>
            ))}
          </div>
        ) : jobs.data ? (
          <EmptyState title={t("analysis.noJobs")} detail={t("analysis.noJobsDetail")} />
        ) : null}
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Persisted runs</p>
            <h2>{t("analysis.latest")}</h2>
          </div>
          <InlineTag>{t("analysis.totalRuns", { count: analysis.data?.total ?? 0 })}</InlineTag>
        </div>
        {analysis.error ? <EmptyState title={t("analysis.loadError")} detail={analysis.error} tone="danger" /> : null}
        {analysis.loading && !analysis.data ? <EmptyState title={t("analysis.loading")} detail={t("analysis.loadingDetail")} /> : null}
        {items.length ? (
          <div className="finding-list">
            {items.map((run) => (
              <article key={run.id} className="finding-card">
                <div className="finding-card-head">
                  <div>
                    <strong>{run.kind}</strong>
                    <span>{run.analyzer} {run.analyzer_version}</span>
                  </div>
                  <InlineTag tone={run.status === "completed" ? "green" : "gold"}>{run.status}</InlineTag>
                </div>
                <div className="detail-meta-strip">
                  <DetailMetaPill label="session" value={run.session_id || "-"} mono />
                  <DetailMetaPill label="trace" value={run.trace_id || "-"} mono />
                  <DetailMetaPill label="input" value={run.input_ref || "-"} mono />
                  <DetailMetaPill label="created" value={formatDateTime(run.created_at)} />
                </div>
                <div className="action-group action-group-start">
                  {run.session_id ? <Link className="ghost-button" to={`/sessions/${encodeURIComponent(run.session_id)}`}>{t("analysis.openSession")}</Link> : null}
                  {run.trace_id ? <Link className="ghost-button" to={`/traces/${encodeURIComponent(run.trace_id)}`}>{t("analysis.openTrace")}</Link> : null}
                </div>
              </article>
            ))}
          </div>
        ) : analysis.data ? (
          <EmptyState title={t("analysis.noRuns")} detail={t("analysis.noRunsDetail")} />
        ) : null}
      </section>
    </div>
  );
}
