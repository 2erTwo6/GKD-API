import { Table, Tag, Typography, message } from "antd";
import { useEffect, useState } from "react";
import { api, fmtTime } from "../api";

interface Log {
  id: number;
  created_at: string;
  key_name: string;
  virtual_model: string;
  winner: string;
  batch_size: number;
  pick_mode: string;
  latency_ms: number;
  status: number;
  detail: string;
}

export default function Logs() {
  const [items, setItems] = useState<Log[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);

  const load = async (p = page) => {
    setLoading(true);
    try {
      const res = await api<{ total: number; items: Log[] }>(`/logs?page=${p}&page_size=20`);
      setItems(res.items);
      setTotal(res.total);
    } catch (e: any) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, [page]);

  const columns = [
    { title: "时间", dataIndex: "created_at", width: 170, render: fmtTime },
    { title: "密钥", dataIndex: "key_name", width: 120 },
    { title: "虚拟模型", dataIndex: "virtual_model", width: 120 },
    {
      title: "命中上游",
      dataIndex: "winner",
      width: 130,
      render: (v: string) => (v ? <Tag color="green">{v}</Tag> : <Tag color="red">无</Tag>),
    },
    {
      title: "每轮并发",
      dataIndex: "batch_size",
      width: 90,
      render: (v: number) => (v <= 0 ? "全部" : v),
    },
    {
      title: "策略",
      dataIndex: "pick_mode",
      width: 100,
      render: (v: string) => (v === "fastest" ? <Tag color="red">最快</Tag> : <Tag color="gold">权重</Tag>),
    },
    { title: "耗时", dataIndex: "latency_ms", width: 90, render: (v: number) => `${v} ms` },
    {
      title: "状态",
      dataIndex: "status",
      width: 80,
      render: (v: number) => <Tag color={v === 200 ? "green" : "red"}>{v}</Tag>,
    },
    {
      title: "详情",
      dataIndex: "detail",
      ellipsis: true,
      render: (v: string) =>
        v ? (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {v}
          </Typography.Text>
        ) : (
          "-"
        ),
    },
  ];

  return (
    <div>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        调用日志
      </Typography.Title>
      <Table
        rowKey="id"
        size="small"
        columns={columns as any}
        dataSource={items}
        loading={loading}
        pagination={{
          current: page,
          pageSize: 20,
          total,
          onChange: (p) => setPage(p),
          showSizeChanger: false,
        }}
      />
    </div>
  );
}
