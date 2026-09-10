import { Dropdown, Layout } from "antd";
import { KeyOutlined, LogoutOutlined, LockOutlined, PartitionOutlined, FileTextOutlined } from "@ant-design/icons";
import { useState, type ReactNode } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { clearToken } from "../api";
import ChangePasswordModal from "../components/ChangePasswordModal";

export default function AdminLayout() {
  const nav = useNavigate();
  const [pwOpen, setPwOpen] = useState(false);

  const menuItems = [
    { key: "/", icon: <PartitionOutlined />, label: "虚拟模型" },
    { key: "/keys", icon: <KeyOutlined />, label: "API 密钥" },
    { key: "/logs", icon: <FileTextOutlined />, label: "调用日志" },
  ];

  const userMenu = {
    items: [
      { key: "pw", icon: <LockOutlined />, label: "修改密码" },
      { type: "divider" as const },
      { key: "out", icon: <LogoutOutlined />, label: "退出登录" },
    ],
    onClick: ({ key }: { key: string }) => {
      if (key === "pw") setPwOpen(true);
      if (key === "out") {
        clearToken();
        nav("/login");
      }
    },
  };

  return (
    <div style={{ display: "flex", minHeight: "100vh" }}>
      <Layout.Sider theme="dark" width={200}>
        <div style={{ color: "#fff", fontSize: 18, fontWeight: 700, padding: "20px 16px 12px" }}>GKD-API</div>
        <div style={{ color: "rgba(255,255,255,.45)", fontSize: 12, padding: "0 16px 8px" }}>OpenAI 兼容网关</div>
        <div style={{ background: "#001529" }}>
          {menuItems.map((m) => (
            <SiderItem key={m.key} to={m.key} icon={m.icon} label={m.label} />
          ))}
        </div>
      </Layout.Sider>
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0 }}>
        <Layout.Header
          style={{
            background: "#fff",
            display: "flex",
            justifyContent: "flex-end",
            alignItems: "center",
            paddingInline: 24,
            boxShadow: "0 1px 4px rgba(0,0,0,.06)",
          }}
        >
          <Dropdown menu={userMenu} trigger={["click"]}>
            <span style={{ cursor: "pointer" }}>管理员 ▾</span>
          </Dropdown>
        </Layout.Header>
        <Layout.Content style={{ padding: 24 }}>
          <Outlet />
        </Layout.Content>
      </div>
      <ChangePasswordModal open={pwOpen} onClose={() => setPwOpen(false)} />
    </div>
  );
}

function SiderItem({ to, icon, label }: { to: string; icon: ReactNode; label: ReactNode }) {
  return (
    <NavLink
      to={to}
      style={({ isActive }) => ({
        display: "flex",
        alignItems: "center",
        gap: 10,
        margin: "4px 8px",
        padding: "10px 12px",
        borderRadius: 6,
        color: "#fff",
        textDecoration: "none",
        background: isActive ? "rgba(64,86,224,.9)" : "transparent",
      })}
    >
      {icon}
      <span>{label}</span>
    </NavLink>
  );
}
