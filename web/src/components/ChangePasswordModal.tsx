import { Button, Form, Input, Modal, message } from "antd";
import { api } from "../api";
import { useState } from "react";

export default function ChangePasswordModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);

  const submit = async () => {
    const v = await form.validateFields();
    if (v.new_password !== v.confirm) {
      message.error("两次输入的新密码不一致");
      return;
    }
    setLoading(true);
    try {
      await api("/change-password", {
        method: "POST",
        body: JSON.stringify({ old_password: v.old_password, new_password: v.new_password }),
      });
      message.success("密码已修改");
      form.resetFields();
      onClose();
    } catch (e: any) {
      message.error(e.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Modal
      title="修改密码"
      open={open}
      onCancel={() => {
        form.resetFields();
        onClose();
      }}
      footer={[
        <Button key="c" onClick={() => onClose()}>
          取消
        </Button>,
        <Button key="s" type="primary" loading={loading} onClick={submit}>
          保存
        </Button>,
      ]}
    >
      <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
        <Form.Item name="old_password" label="旧密码" rules={[{ required: true, message: "请输入旧密码" }]}>
          <Input.Password />
        </Form.Item>
        <Form.Item
          name="new_password"
          label="新密码"
          rules={[{ required: true, message: "请输入新密码" }, { min: 6, message: "至少 6 位" }]}
        >
          <Input.Password />
        </Form.Item>
        <Form.Item name="confirm" label="确认新密码" rules={[{ required: true, message: "请再次输入新密码" }]}>
          <Input.Password />
        </Form.Item>
      </Form>
    </Modal>
  );
}
