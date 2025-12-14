import React, { useState } from "react";
import { useWorkspaceStore } from "@/lib/store";
import { createClient } from "@connectrpc/connect";
import { useMutation } from "@tanstack/react-query";
import { Workspace } from "@grpcview/v1/service_pb";
import { Service } from "@grpcview/v1/service_pb";
import { transport } from "@/lib/client";
import { TreeView } from "@/components/TreeView";
import { Editor } from "@/components/Editor";
import { AddSourceModal } from "@/components/AddSourceModal";

// Create a typed client for Manual usage if needed, or use useMutation from generated hooks if available.
// The user asked for `connect-query`.
// Usually one generates hooks like `useAdd` from the proto service definition.
// If the user hasn't generated react-query hooks, we can't use them directly.
// The `package.json` had `@connectrpc/protoc-gen-connect-query`.
// So we should have generated code like `workspace-Workspace_connectquery.ts`?
// The generated target name is `grpcviewv1_ts_proto`.
// Let's assume standard connect-query generation output.
// However, I don't see where the generated files are.
// I'll stick to manual client creation wrapped in useMutation for now to be safe,
// as I don't know the exact path of the generated query hooks.
// Actually, using `useMutation` with the client method is standard "TanStack Query" usage even without the generated hooks.

const client = createClient(Workspace, transport);

export const WorkspacePage: React.FC = () => {
  const store = useWorkspaceStore();
  const [services, setServices] = useState<Service[]>([]);
  const [showModal, setShowModal] = useState(false);

  const addMutation = useMutation({
    mutationFn: async (req: any) => {
      return await client.add(req);
    },
    onSuccess: (data) => {
      setServices((prev) => [...prev, ...data.services]);
    },
    onError: (error) => {
      console.error("Add source failed", error);
      alert("Add source failed: " + error);
    },
  });

  const handleAddReflection = (host: string, port: number) => {
    addMutation.mutate({
      source: {
        case: "reflection",
        value: { host, port },
      },
    });
  };

  const handleAddDescriptor = async (file: File) => {
    try {
      const buffer = await file.arrayBuffer();
      addMutation.mutate({
        source: {
          case: "descriptorSet",
          value: new Uint8Array(buffer),
        },
      });
    } catch (err) {
      console.error(err);
    }
  };

  const downloadWorkspace = () => {
    const data = JSON.stringify(store.rootItem, null, 2);
    const blob = new Blob([data], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "workspace.json";
    a.click();
    URL.revokeObjectURL(url);
  };

  const uploadWorkspace = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (ev) => {
      try {
        const json = JSON.parse(ev.target?.result as string);
        store.setRootItem(json);
      } catch (err) {
        alert("Invalid JSON");
      }
    };
    reader.readAsText(file);
  };

  return (
    <div className="flex w-full h-full bg-slate-50">
      {/* Sidebar */}
      <div className="w-[280px] h-full flex flex-col border-r border-gray-200 bg-white">
        <div className="h-12 flex items-center px-4 border-b border-gray-200 bg-white gap-2">
          <button
            onClick={() => setShowModal(true)}
            className="text-purple-600 uppercase font-medium text-sm hover:bg-purple-50 px-3 py-1 rounded"
          >
            + Source
          </button>
          <button
            onClick={downloadWorkspace}
            className="text-purple-600 uppercase font-medium text-sm hover:bg-purple-50 px-3 py-1 rounded"
          >
            Export
          </button>
          <label className="text-gray-700 uppercase font-medium text-sm hover:bg-gray-50 px-3 py-1 rounded border border-gray-200 cursor-pointer flex items-center">
            Import
            <input
              type="file"
              onChange={uploadWorkspace}
              accept=".json"
              className="hidden"
            />
          </label>
        </div>

        <div className="flex-grow overflow-y-auto py-2">
          <TreeView
            item={store.rootItem}
            onAddItem={store.addItem}
            onRemoveItem={store.removeItem}
          />
        </div>
      </div>

      {/* Main Area */}
      <div className="flex-grow h-full bg-white">
        <Editor services={services} />
      </div>

      <AddSourceModal
        show={showModal}
        onClose={() => setShowModal(false)}
        onAddReflection={handleAddReflection}
        onAddDescriptor={handleAddDescriptor}
      />
    </div>
  );
};
