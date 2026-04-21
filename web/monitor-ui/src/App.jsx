import React from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { RequestsPage } from "./routes/RequestsPage";
import { RoutingPage } from "./routes/RoutingPage";
import { SessionDetailPage } from "./routes/SessionDetailPage";
import { SessionsPage } from "./routes/SessionsPage";
import { TraceDetailPage } from "./routes/TraceDetailPage";
import { UpstreamDetailPage } from "./routes/UpstreamDetailPage";

function App() {
  return (
    <Routes>
      <Route path="/" element={<Navigate to="/requests" replace />} />
      <Route path="/requests" element={<RequestsPage />} />
      <Route path="/sessions" element={<SessionsPage />} />
      <Route path="/routing" element={<RoutingPage />} />
      <Route path="/sessions/:sessionID" element={<SessionDetailPage />} />
      <Route path="/upstreams/:upstreamID" element={<UpstreamDetailPage />} />
      <Route path="/traces/:traceID" element={<TraceDetailPage />} />
    </Routes>
  );
}

export default App;
