import React, { useState, useEffect } from "react";
import { useWorkspaceStore, Item } from "@/lib/store";
import { createClient } from "@connectrpc/connect";
import { useMutation } from "@tanstack/react-query";
import {
  AddDescriptorSourceRequestSchema,
  Workspace,
} from "@grpcview/v1/service_pb"; // The service definition
// Generated types
import { type AddDescriptorSourceRequest } from "@grpcview/v1/service_pb";

import { transport } from "@/lib/client";
import { TreeView } from "@/components/TreeView";
import { Editor } from "@/components/Editor";
import { AddSourceModal } from "@/components/AddSourceModal";
import { RequestSelectorModal } from "@/components/RequestSelectorModal";
import { create } from "@bufbuild/protobuf";

const client = createClient(Workspace, transport);

export const WorkspacePage: React.FC = () => {
  const store = useWorkspaceStore();
  const [showSourceModal, setShowSourceModal] = useState(false);
  const [showRequestModal, setShowRequestModal] = useState(false);

  const [activeItem, setActiveItem] = useState<Item | null>(null);
  const [requestTargetParent, setRequestTargetParent] = useState<Item | null>(
    null
  );

  // Initial Load
  // We should probably use useQuery here, but manual fetch for now as per plan
  useEffect(() => {
    // Fetch workspace
    const fetchWorkspace = async () => {
      try {
        const res = await client.getWorkspace({ name: "default" });
        if (res.workspace) {
          if (res.workspace.services) store.setServices(res.workspace.services);
          // If we want to sync items from server:
          // store.setRootItems(res.workspace.items);
          // But we are persisting locally, so maybe we only want services?
          // The requirement says "tree viewer ... is displayed with that data".
          // So we should probably respect server data if present.
          if (res.workspace.items && res.workspace.items.length > 0) {
            store.setRootItems(res.workspace.items);
          }
        }
      } catch (e) {
        console.error("Failed to load workspace", e);
      }
    };
    fetchWorkspace();
  }, []);

  const addMutation = useMutation({
    mutationFn: async (req: AddDescriptorSourceRequest) => {
      return await client.addDescriptorSource(req);
    },
    onSuccess: (data) => {
      if (data.workspace?.services) {
        store.setServices(data.workspace.services);
      }
    },
    onError: (error) => {
      console.error("Add source failed", error);
      alert("Add source failed: " + error);
    },
  });

  const handleAddReflection = (host: string, port: number) => {
    const req = create(AddDescriptorSourceRequestSchema, {
      source: {
        case: "reflection",
        value: {
          host,
          port,
        },
      },
    });
    addMutation.mutate(req);
  };

  const handleAddDescriptor = async (file: File) => {
    try {
      const buffer = await file.arrayBuffer();
      const req = create(AddDescriptorSourceRequestSchema, {
        source: {
          case: "descriptorSet",
          value: new Uint8Array(buffer),
        },
      });
      addMutation.mutate(req);
    } catch (err) {
      console.error(err);
    }
  };

  const handleStartAddRequest = (parent: Item) => {
    setRequestTargetParent(parent);
    setShowRequestModal(true);
  };

  const handleRequestSelected = (service: any, method: any) => {
    if (!requestTargetParent) return;

    store.addItem(requestTargetParent, {
      name: method.name,
      id: crypto.randomUUID(),
      content: {
        case: "request",
        value: {
          service: service, // Store reduced info or full? Store expects Service
          method: method,
          request: "{}",
        },
      },
    });
    setRequestTargetParent(null);
  };

  const handleSelectItem = (item: Item) => {
    if (item.content.case === "request") {
      setActiveItem(item);
    }
  };

  // Editor Data
  const editorData =
    activeItem?.content.case === "request"
      ? activeItem.content.value.request
      : "{}";

  const editorService =
    activeItem?.content.case === "request"
      ? `${activeItem.content.value.service?.package}.${activeItem.content.value.service?.name}`
      : undefined;

  const editorMethod =
    activeItem?.content.case === "request"
      ? activeItem.content.value.method?.name
      : undefined;

  return (
    <div className="flex w-full h-full bg-slate-50">
      {/* Sidebar */}
      <div className="w-[300px] h-full flex flex-col border-r border-gray-200 bg-white">
        <div className="h-12 flex items-center px-4 border-b border-gray-200 bg-white gap-2">
          <button
            onClick={() => setShowSourceModal(true)}
            className="text-purple-600 uppercase font-medium text-xs hover:bg-purple-50 px-2 py-1 rounded border border-transparent hover:border-purple-100"
          >
            + Source
          </button>
        </div>

        <div className="flex-grow overflow-y-auto py-2">
          {store.rootItems.map((item, idx) => (
            <TreeView
              key={item.id}
              item={item}
              onAddItem={store.addItem}
              onRemoveItem={store.removeItem}
              onRenameItem={store.renameItem}
              onSelect={handleSelectItem}
              onStartAddRequest={handleStartAddRequest}
            />
          ))}
        </div>
      </div>

      {/* Main Area */}
      <div className="flex-grow h-full bg-white relative">
        {activeItem ? (
          <Editor
            services={store.services}
            data={editorData}
            onChange={(val) => store.updateRequestData(activeItem, val)}
            currentService={editorService}
            currentMethod={editorMethod}
          />
        ) : (
          <div className="flex items-center justify-center h-full text-gray-400">
            Select a request to edit
          </div>
        )}
      </div>

      <AddSourceModal
        show={showSourceModal}
        onClose={() => setShowSourceModal(false)}
        onAddReflection={handleAddReflection}
        onAddDescriptor={handleAddDescriptor}
      />

      <RequestSelectorModal
        show={showRequestModal}
        services={store.services}
        onClose={() => setShowRequestModal(false)}
        onSelect={handleRequestSelected}
      />
    </div>
  );
};
