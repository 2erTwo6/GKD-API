import { ArrowLeftOutlined, PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Card,
  Col,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api } from "../api";

interface RealModel {
  id: number;
  virtual_model_id: number;
  name: string;
  base_url: string;
  api_key: string;
  protocol: string;
  weight: number;
  override_json: string;
  enabled: boolean;
}

interface VM {
  id: number;
  name: string;
  batch_size: number;
  batch_wait_ms: number;
  pick_mode: string;
  max_wait_ms: number;
  enabled: boolean;
  real_models: RealModel[];
}

const emptyReal = {
  name: "",
  base_url: "",
  api_key: "",
  protocol: "openai",
  weight: 1,
  override_json: "",
  enabled: true,
};

export default function VirtualModelDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const [vm, setVM] = useState<VM | null>(null);
  const [ruleForm] = Form.useForm();
  const [realForm] = Form.useForm();
  const [modalOpen, setModalOpen] = useState(false);
  const [editingReal, setEditingReal] = useState<RealModel | null>(null);
  const [saving, setSaving] = useState(false);

  const load = async () => {
    try {
      const res = await api<VM>(`/virtual-models/${id}`);
      setVM(res);
      ruleForm.setFieldsValue(res);
    } catch (e: any) {
      message.error(e.message);
    }
  };

  useEffect(() => {
    load();
  }, [id]);

  const saveRule = async () => {
    const v = await ruleForm.validateFields();
    setSaving(true);
    try {
      await api(`/virtual-models/${id}`, { method: "PUT", body: JSON.stringify(v) });
      message.success("路由规则已保存");
      load();
    } catch (e: any) {
      message.error(e.message);
    } finally {
      setSaving(false);
    }
  };

  const openRealModal = (rm: RealModel | null) => {
    setEditingReal(rm);
    realForm.setFieldsValue(rm ? { ...rm } : emptyReal);
    setModalOpen(true);
  };

  const submitReal = async () => {
    const v = await realForm.validateFields();
    if (v.override_json && v.override_json.trim()) {
      try {
        const parsed = JSON.parse(v.override_json);
        if (typeof parsed !== "object" || Array.isArray(parsed) || parsed === null) {
          throw new Error("必须是 JSON 对象");
        }
      } catch (e: any) {
        message.error("override_json 不是合法 JSON 对象: " + e.message);
        return;
      }
    }
    try {
      if (editingReal) {
        await api(`/real-models/${editingReal.id}`, { method: "PUT", body: JSON.stringify(v) });
        message.success("已更新");
      } else {
        await api(`/virtual-models/${id}/real-models`, { method: "POST", body: JSON.stringify(v) });
        message.success("已添加");
      }
      setModalOpen(false);
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const removeReal = async (rmId: number) => {
    try {
      await api(`/real-models/${rmId}`, { method: "DELETE" });
      message.success("已删除");
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const toggleReal = async (rm: RealModel, enabled: boolean) => {
    try {
      await api(`/real-models/${rm.id}`, { method: "PUT", body: JSON.stringify({ enabled }) });
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const realColumns = [
    { title: "上游模型名", dataIndex: "name", width: 140 },
    {
      title: "协议",
      dataIndex: "protocol",
      width: 90,
      render: (v: string) => (v === "gemini" ? <Tag color="green">Gemini</Tag> : <Tag color="geekblue">OpenAI</Tag>),
    },
    {
      title: "Base URL",
      dataIndex: "base_url",
      ellipsis: true,
    },
    { title: "权重", dataIndex: "weight", width: 70 },
    {
      title: "参数覆写",
      dataIndex: "override_json",
      width: 110,
      render: (v: string) =>
        v ? (
          <Typography.Text code style={{ fontSize: 12 }}>
            {v.length > 18 ? v.slice(0, 18) + "…" : v}
          </Typography.Text>
        ) : (
          <span style={{ color: "#bbb" }}>-</span>
        ),
    },
    {
      title: "启用",
      dataIndex: "enabled",
      width: 80,
      render: (v: boolean, r: RealModel) => <Switch checked={v} onChange={(c) => toggleReal(r, c)} />,
    },
    {
      title: "操作",
      width: 140,
      render: (_: any, r: RealModel) => (
        <Space>
          <Button size="small" type="link" onClick={() => openRealModal(r)}>
            编辑
          </Button>
          <Popconfirm title="确定删除该真实模型？" onConfirm={() => removeReal(r.id)}>
            <Button size="small" type="link" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  if (!vm) return null;

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => nav("/")} style={{ marginRight: 8 }}>
          返回
        </Button>
        <Typography.Title level={4} style={{ margin: 0, display: "inline" }}>
          虚拟模型：{vm.name}
        </Typography.Title>
      </div>

      <Row gutter={16}>
        <Col span={8}>
          <Card title="路由规则" size="small">
            <Form form={ruleForm} layout="vertical">
              <Form.Item
                name="pick_mode"
                label="选择策略"
                tooltip="最快响应：并发中谁先响应谁胜出；权重优先：每轮窗口结束时取已响应中权重最高者"
              >
                <Select
                  options={[
                    { value: "fastest", label: "最快响应（fastest）" },
                    { value: "weight", label: "权重优先（weight）" },
                  ]}
                />
              </Form.Item>
              <Form.Item name="batch_size" label="每轮并发请求数" tooltip="0 表示一次请求全部真实模型">
                <InputNumber min={0} style={{ width: "100%" }} />
              </Form.Item>
              <Form.Item name="batch_wait_ms" label="每轮等待窗口 (ms)">
                <InputNumber min={100} style={{ width: "100%" }} />
              </Form.Item>
              <Form.Item name="max_wait_ms" label="兜底超时 (ms)">
                <InputNumber min={100} style={{ width: "100%" }} />
              </Form.Item>
              <Form.Item name="enabled" label="启用" valuePropName="checked">
                <Switch />
              </Form.Item>
              <Button type="primary" block loading={saving} onClick={saveRule}>
                保存规则
              </Button>
            </Form>
          </Card>
        </Col>
        <Col span={16}>
          <Card
            title="真实模型"
            size="small"
            extra={
              <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => openRealModal(null)}>
                添加真实模型
              </Button>
            }
          >
            <Table rowKey="id" size="small" columns={realColumns as any} dataSource={vm.real_models || []} pagination={false} />
          </Card>
        </Col>
      </Row>

      <Modal
        title={editingReal ? "编辑真实模型" : "添加真实模型"}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={submitReal}
        width={560}
        destroyOnClose
      >
        <Form form={realForm} layout="vertical" style={{ marginTop: 12 }}>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item
                name="name"
                label="上游模型名"
                tooltip="发送给上游的 model 参数，例如 gpt-4o-mini / gemini-1.5-flash"
                rules={[{ required: true }]}
              >
                <Input />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="protocol" label="协议" rules={[{ required: true }]}>
                <Select
                  options={[
                    { value: "openai", label: "OpenAI 格式" },
                    { value: "gemini", label: "Gemini Generate Content" },
                  ]}
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name="base_url"
            label="Base URL"
            tooltip="OpenAI: https://api.openai.com/v1 ；Gemini: https://generativelanguage.googleapis.com"
            rules={[{ required: true }]}
          >
            <Input placeholder="https://..." />
          </Form.Item>
          <Form.Item name="api_key" label="API Key">
            <Input.Password placeholder="上游的密钥" />
          </Form.Item>
          <Row gutter={12}>
            <Col span={6}>
              <Form.Item name="weight" label="权重" tooltip="越大越优先">
                <InputNumber min={0} style={{ width: "100%" }} />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="enabled" label="启用" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name="override_json"
            label="参数覆写（JSON Merge Patch）"
            tooltip='发往该上游前对 OpenAI 格式请求体做合并覆写；值为 null 可删除字段。例如 {"temperature":0.3,"reasoning_effort":null}'
          >
            <Input.TextArea rows={4} placeholder='{"temperature": 0.3}' style={{ fontFamily: "monospace" }} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
