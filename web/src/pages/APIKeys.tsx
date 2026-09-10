import { PlusOutlined, CopyOutlined } from "@ant-design/icons";
import { Button, Form, Input, Modal, Popconfirm, Space, Switch, Table, Typography, message } from "antd";
import { useEffect, useState } from "react";
import { api, fmtTime } from "../api";

interface Key {
  id: number;
  key: string;
  name: string;
  enabled: boolean;
  last_used_at: string | null;
  created_at: string;
}

export default function APIKeys() {
  const [items, setItems] = useState<Key[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [newKey, setNewKey] = useState<Key | null>(null);
  const [form] = Form.useForm();

  const load = async () => {
    setLoading(true);
    try {
      const res = await api<{ items: Key[] }>("/api-keys");
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

  const create = async () => {
    const v = await form.validateFields();
    try {
      const res = await api<Key>("/api-keys", { method: "POST", body: JSON.stringify(v) });
      setNewKey(res);
      setOpen(false);
      form.resetFields();
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const update = async (k: Key, patch: any) => {
    try {
      await api(`/api-keys/${k.id}`, { method: "PUT", body: JSON.stringify(patch) });
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const remove = async (id: number) => {
    try {
      await api(`/api-keys/${id}`, { method: "DELETE" });
      message.success("已删除");
      load();
    } catch (e: any) {
      message.error(e.message);
    }
  };

  const copy = (text: string) => {
    navigator.clipboard.writeText(text).then(
      () => message.success("已复制"),
      () => message.error("复制失败"),
    );
  };

  const mask = (k: string) => k.slice(0, 10) + "…" + k.slice(-4);

  const columns = [
    { title: "名称", dataIndex: "name", width: 180 },
    {
      title: "Key",
      dataIndex: "key",
      render: (v: string) => (
        <Space>
          <Typography.Text code copyable={{ text: v }} style={{ fontFamily: "monospace" }}>
            {mask(v)}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: "启用",
      dataIndex: "enabled",
      width: 80,
      render: (v: boolean, r: Key) => <Switch checked={v} onChange={(c) => update(r, { enabled: c })} />,
    },
    { title: "最后使用", dataIndex: "last_used_at", width: 180, render: fmtTime },
    { title: "创建时间", dataIndex: "created_at", width: 180, render: fmtTime },
    {
      title: "操作",
      width: 160,
      render: (_: any, r: Key) => (
        <Space>
          <Button size="small" type="link" onClick={() => copy(r.key)} icon={<CopyOutlined />}>
            复制
          </Button>
          <Popconfirm title="删除后客户端将无法再使用该密钥，确定？" onConfirm={() => remove(r.id)}>
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
          API 密钥
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            form.resetFields();
            setOpen(true);
          }}
        >
          新建密钥
        </Button>
      </div>
      <Table rowKey="id" columns={columns as any} dataSource={items} loading={loading} pagination={false} />

      <Modal title="新建密钥" open={open} onCancel={() => setOpen(false)} onOk={create} destroyOnClose>
        <Form form={form} layout="vertical" style={{ marginTop: 12 }}>
          <Form.Item name="name" label="名称" tooltip="便于辨认用途，例如“手机APP”">
            <Input placeholder="例如 mobile-app" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="密钥已创建"
        open={!!newKey}
        onCancel={() => setNewKey(null)}
        footer={[
          <Button
            key="ok"
            type="primary"
            onClick={() => {
              copy(newKey!.key);
              setNewKey(null);
            }}
          >
            复制并关闭
          </Button>,
        ]}
      >
        <Typography.Paragraph>请立即保存，此密钥之后不再完整显示：</Typography.Paragraph>
        <Typography.Text code copyable style={{ fontFamily: "monospace", wordBreak: "break-all" }}>
          {newKey?.key}
        </Typography.Text>
      </Modal>
    </div>
  );
}
