import { PlusOutlined } from "@ant-design/icons";
import {
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api";

interface RealModel {
  id: number;
  name: string;
  protocol: string;
  weight: number;
  enabled: boolean;
  base_url: string;
  override_json: string;
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

export default function VirtualModels() {
  const nav = useNavigate();
  const [items, setItems] = useState<VM[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<VM | null>(null);
  const [form] = Form.useForm();

  const load = async () => {
    setLoading(true);
    try {
      const res = await api<{ items: VM[] }>("/virtual-models");
      setItems(res.items);
    } catch (e: any) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const openCreate = () => {
    setEditing(null);
    form.setFieldsValue({ name: "", batch_size: 0, batch_wait_ms: 3000, pick_mode: "fastest", max_wait_ms: 60000 });
    setModalOpen(true);
  };

  const openEdit = (vm: VM) => {
    setEditing(vm);
    form.setFieldsValue(vm);
    setModalOpen(true);
  };

  const submit = async () => {
    const v = await form.validateFields();
    try {
      if (editing) {
        await api(`/virtual-models/${editing.id}`, { method: "PUT", body: JSON.stringify(v) });
        message.success("已更新");
      } else {
        await api("/virtual-models", { method: "POST", body: JSON.stringify(v) });
        message.success("已创建");
      }
      setModalOpen(false);
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const remove = async (id: number) => {
    try {
      await api(`/virtual-models/${id}`, { method: "DELETE" });
      message.success("已删除");
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const toggle = async (vm: VM, enabled: boolean) => {
    try {
      await api(`/virtual-models/${vm.id}`, { method: "PUT", body: JSON.stringify({ enabled }) });
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const columns = [
    { title: "名称", dataIndex: "name", render: (v: string, r: VM) => <Link to={`/models/${r.id}`}>{v}</Link> },
    {
      title: "每轮并发数",
      dataIndex: "batch_size",
      width: 110,
      render: (v: number) => (v <= 0 ? <Tag color="blue">一次全部</Tag> : v),
    },
    { title: "每轮等待", dataIndex: "batch_wait_ms", width: 100, render: (v: number) => `${v} ms` },
    {
      title: "选择策略",
      dataIndex: "pick_mode",
      width: 150,
      render: (v: string) =>
        v === "fastest" ? <Tag color="red">最快响应</Tag> : <Tag color="gold">权重优先</Tag>,
    },
    { title: "兜底超时", dataIndex: "max_wait_ms", width: 110, render: (v: number) => `${v} ms` },
    { title: "真实模型", width: 90, render: (_: any, r: VM) => `${(r.real_models || []).length} 个` },
    {
      title: "启用",
      dataIndex: "enabled",
      width: 80,
      render: (v: boolean, r: VM) => <Switch checked={v} onChange={(c) => toggle(r, c)} />,
    },
    {
      title: "操作",
      width: 180,
      render: (_: any, r: VM) => (
        <Space>
          <Button size="small" type="link" onClick={() => nav(`/models/${r.id}`)}>
            详情
          </Button>
          <Button size="small" type="link" onClick={() => openEdit(r)}>
            编辑
          </Button>
          <Popconfirm title="删除后其真实模型也会一并删除，确定？" onConfirm={() => remove(r.id)}>
            <Button size="small" type="link" danger>
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          虚拟模型
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
          新建虚拟模型
        </Button>
      </div>
      <Table rowKey="id" columns={columns as any} dataSource={items} loading={loading} pagination={false} />
      <Modal
        title={editing ? "编辑虚拟模型" : "新建虚拟模型"}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={submit}
        destroyOnClose
      >
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="name" label="模型名称（客户端调用时使用的 model）" rules={[{ required: true }]}>
            <Input placeholder="例如 free" />
          </Form.Item>
          <Form.Item
            name="pick_mode"
            label="选择策略"
            rules={[{ required: true }]}
            tooltip="最快响应：并发中谁先响应谁胜出；权重优先：每轮窗口结束时取已响应中权重最高者"
          >
            <Select
              options={[
                { value: "fastest", label: "最快响应（fastest）" },
                { value: "weight", label: "权重优先（weight）" },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="batch_size"
            label="每轮并发请求数"
            tooltip="按权重降序分批发送；0 表示一次请求全部真实模型"
          >
            <InputNumber min={0} style={{ width: "100%" }} />
          </Form.Item>
          <Form.Item name="batch_wait_ms" label="每轮等待窗口 (ms)" tooltip="窗口内无人响应则取消本轮，进入下一轮">
            <InputNumber min={100} style={{ width: "100%" }} />
          </Form.Item>
          <Form.Item
            name="max_wait_ms"
            label="兜底超时 (ms)"
            tooltip="整场竞赛的总时间上限，超时后向客户端返回 503"
          >
            <InputNumber min={100} style={{ width: "100%" }} />
          </Form.Item>
          <Tooltip title="仅编辑时可修改启用状态">
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Tooltip>
        </Form>
      </Modal>
    </div>
  );
}
