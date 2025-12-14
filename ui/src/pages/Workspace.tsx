import React, { useState, useEffect } from "react";
import { useWorkspaceStore, ItemWithPath } from "@/lib/store";
import { AddDescriptorSourceRequestSchema } from "@grpcview/v1/service_pb";
import { create } from "@bufbuild/protobuf";

import { TreeView } from "@/components/TreeView";
import { Editor } from "@/components/Editor";
import { AddSourceModal } from "@/components/AddSourceModal";
import { RequestSelectorModal } from "@/components/RequestSelectorModal";
import { Service, Method } from "@grpcview/v1/workspace_pb";

export const WorkspacePage: React.FC = () => {
  const store = useWorkspaceStore();
  const [showSourceModal, setShowSourceModal] = useState(false);
  const [showRequestModal, setShowRequestModal] = useState(false);

  const [activeItem, setActiveItem] = useState<ItemWithPath | null>(null);
  const [requestTargetParent, setRequestTargetParent] =
    useState<ItemWithPath | null>(null);

  useEffect(() => {
    store.loadWorkspace();
  }, []);

  const handleAddReflection = async (host: string, port: number) => {
    const req = create(AddDescriptorSourceRequestSchema, {
      source: {
        case: "reflection",
        value: {
          host,
          port,
        },
      },
    });
    await store.addDescriptorSource(req);
    setShowSourceModal(false);
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
      await store.addDescriptorSource(req);
      setShowSourceModal(false);
    } catch (err) {
      console.error(err);
    }
  };

  const handleStartAddRequest = (parent: ItemWithPath | null) => {
    setRequestTargetParent(parent);
    setShowRequestModal(true);
  };

  const handleRequestSelected = (service: Service, method: Method) => {
    const serviceName = service.package + "." + service.name;
    const methodName = method.name;

    store.createRequest(
      requestTargetParent,
      methodName,
      serviceName,
      methodName
    );

    setRequestTargetParent(null);
    setShowRequestModal(false);
  };

  const handleAddItem = (parent: ItemWithPath, item: ItemWithPath) => {
    if (item.item.content.case === "folder") {
      store.createFolder(parent, item.item.name);
    }
  };

  const handleRemoveItem = (parent: ItemWithPath | null, index: number) => {
    let child: ItemWithPath | undefined;
    if (!parent) {
      child = store.rootItems[index];
    } else if (parent.children) {
      child = parent.children[index];
    }

    if (child) {
      store.deleteItem(child);
    }
  };

  const handleRenameItem = (item: ItemWithPath, newName: string) => {
    store.renameItem(item, newName);
  };

  const handleSelectItem = (item: ItemWithPath) => {
    if (item.item.content.case === "request") {
      setActiveItem(item);
    }
  };

  // Editor Data - decode bytes to string for editing
  const getEditorData = (): string => {
    if (activeItem?.item.content.case === "request") {
      const req = activeItem.item.content.value;
      if (req.draftBody && req.draftBody.length > 0) {
        return new TextDecoder().decode(req.draftBody);
      }
    }
    return "{}";
  };

  const editorData = getEditorData();

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
          <button
            onClick={() => {
              const name = prompt("Folder Name");
              if (name) store.createFolder(null, name);
            }}
            className="text-gray-600 uppercase font-medium text-xs hover:bg-gray-50 px-2 py-1 rounded border border-transparent hover:border-gray-200"
          >
            + Folder
          </button>
          <button
            onClick={() => handleStartAddRequest(null)}
            className="text-gray-600 uppercase font-medium text-xs hover:bg-gray-50 px-2 py-1 rounded border border-transparent hover:border-gray-200"
          >
            + Request
          </button>
        </div>

        <div className="flex-grow overflow-y-auto py-2">
          {store.rootItems.map((item, index) => (
            <TreeView
              key={`${item.item.name}-${index}`}
              item={item}
              onAddItem={handleAddItem}
              onRemoveItem={handleRemoveItem}
              onRenameItem={handleRenameItem}
              onSelect={handleSelectItem}
              onStartAddRequest={handleStartAddRequest}
            />
          ))}
        </div>
      </div>

      {/* Main Area */}
      <div className="flex-grow h-full bg-white relative">
        {activeItem?.item.content.case === "request" ? (
          <Editor
            services={store.services}
            data={editorData}
            onChange={(val: string) => store.updateRequestData(activeItem, val)}
            currentMethod={{
              service: activeItem.item.content.value.service,
              method: activeItem.item.content.value.method,
            }}
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
