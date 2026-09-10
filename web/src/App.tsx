import { Navigate, Route, BrowserRouter as Router, Routes } from "react-router-dom";
import type { ReactNode } from "react";
import { getToken } from "./api";
import Layout from "./pages/Layout";
import Login from "./pages/Login";
import VirtualModels from "./pages/VirtualModels";
import VirtualModelDetail from "./pages/VirtualModelDetail";
import APIKeys from "./pages/APIKeys";
import Logs from "./pages/Logs";

function RequireAuth({ children }: { children: ReactNode }) {
  if (!getToken()) return <Navigate to="/login" replace />;
  return children;
}

export default function App() {
  return (
    <Router>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          path="/"
          element={
            <RequireAuth>
              <Layout />
            </RequireAuth>
          }
        >
          <Route index element={<VirtualModels />} />
          <Route path="models/:id" element={<VirtualModelDetail />} />
          <Route path="keys" element={<APIKeys />} />
          <Route path="logs" element={<Logs />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Router>
  );
}
