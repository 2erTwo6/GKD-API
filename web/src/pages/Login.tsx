import { Button, Card, Form, Input, message } from "antd";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, setToken } from "../api";

export default function Login() {
  const nav = useNavigate();
  const [loading, setLoading] = useState(false);

  const submit = async (v: any) => {
    setLoading(true);
    try {
      const res = await api<{ token: string }>("/login", {
        method: "POST",
        body: JSON.stringify(v),
      });
      setToken(res.token);
      nav("/", { replace: true });
    } catch (e: any) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "linear-gradient(135deg, #283a8f 0%, #4056e0 100%)",
      }}
    >
      <Card style={{ width: 380, boxShadow: "0 12px 40px rgba(0,0,0,.25)" }}>
        <div style={{ fontSize: 24, fontWeight: 700, textAlign: "center", marginBottom: 4 }}>GKD-API</div>
        <div style={{ color: "#999", textAlign: "center", marginBottom: 20 }}>OpenAI 兼容网关 · 管理后台</div>
        <Form layout="vertical" onFinish={submit}>
          <Form.Item name="username" label="用户名" rules={[{ required: true, message: "请输入用户名" }]}>
            <Input autoFocus />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, message: "请输入密码" }]}>
            <Input.Password />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  );
}
