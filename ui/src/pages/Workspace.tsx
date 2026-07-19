import React, { useState, useEffect } from "react";
import { Send } from "lucide-react";
import { useWorkspaceStore, ItemWithPath, itemKey } from "@/lib/store";
import { AddDescriptorSourceRequestSchema } from "@grpcview/v1/service_pb";
import { create } from "@bufbuild/protobuf";

import { TreeView } from "@/components/TreeView";
import { Editor } from "@/components/Editor";
import { AddSourceModal } from "@/components/AddSourceModal";
import { RequestSelectorModal } from "@/components/RequestSelectorModal";
import {
  MetadataEditor,
  MetadataRow,
  rowsToObject,
  objectToRows,
} from "@/components/MetadataEditor";
import { ResponsePanel } from "@/components/ResponsePanel";
import { Service, Method } from "@grpcview/v1/workspace_pb";

export const WorkspacePage: React.FC = () => {
  const store = useWorkspaceStore();
  const [showSourceModal, setShowSourceModal] = useState(false);
  const [showRequestModal, setShowRequestModal] = useState(false);

  const [activeItem, setActiveItem] = useState<ItemWithPath | null>(null);
  const [requestTargetParent, setRequestTargetParent] =
    useState<ItemWithPath | null>(null);

  // Local editor state for the active request. It is the source of truth while
  // editing (the stored Item goes stale after each reload), initialized when
  // the selected request changes and persisted back on edit.
  const [body, setBody] = useState<string>("{}");
  const [metadataRows, setMetadataRows] = useState<MetadataRow[]>([]);
  const [activeTab, setActiveTab] = useState<"body" | "metadata">("body");

  const activeKey = activeItem ? itemKey(activeItem) : null;

  useEffect(() => {
    store.loadWorkspace();
  }, []);

  useEffect(() => {
    if (activeItem?.item.content.case === "request") {
      const req = activeItem.item.content.value;
      setBody(req.draftBody || "{}");
      setMetadataRows(objectToRows(req.draftMetadata));
      setActiveTab("body");
    }
  }, [activeKey]);

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

    store.createRequest(requestTargetParent, methodName, serviceName, methodName);

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

  const handleBodyChange = (val: string) => {
    setBody(val);
    if (activeItem) store.updateRequestData(activeItem, val);
  };

  const handleMetadataChange = (rows: MetadataRow[]) => {
    setMetadataRows(rows);
    if (activeItem) store.updateRequestMetadata(activeItem, rowsToObject(rows));
  };

  const handleSend = () => {
    if (activeItem) store.invoke(activeItem, body, rowsToObject(metadataRows));
  };

  const activeRequest =
    activeItem?.item.content.case === "request"
      ? activeItem.item.content.value
      : null;
  const response = activeKey ? store.responses[activeKey] : undefined;
  const responseError = activeKey ? store.responseErrors[activeKey] : undefined;
  const isInvoking = activeKey ? !!store.invoking[activeKey] : false;

  const tabClass = (tab: "body" | "metadata") =>
    `px-4 py-2 text-sm font-medium border-b-2 ${
      activeTab === tab
        ? "border-purple-600 text-purple-700"
        : "border-transparent text-gray-500 hover:text-gray-700"
    }`;

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
        {activeRequest ? (
          <div className="flex flex-col h-full">
            {/* Request header */}
            <div className="h-12 flex items-center gap-3 px-4 border-b border-gray-200 shrink-0">
              <div className="flex flex-col leading-tight min-w-0">
                <span className="text-sm font-medium text-gray-800 truncate">
                  {activeRequest.method}
                </span>
                <span className="text-xs text-gray-400 truncate">
                  {activeRequest.service}
                </span>
              </div>
              <button
                onClick={handleSend}
                disabled={isInvoking}
                className="ml-auto flex items-center gap-2 bg-purple-600 text-white px-4 py-1.5 rounded hover:bg-purple-700 disabled:opacity-50 uppercase text-sm font-medium"
              >
                <Send size={16} />
                {isInvoking ? "Sending…" : "Send"}
              </button>
            </div>

            {/* Request / Response split */}
            <div className="flex-grow flex overflow-hidden">
              {/* Request */}
              <div className="w-1/2 flex flex-col border-r border-gray-200">
                <div className="flex border-b border-gray-200 shrink-0">
                  <button
                    className={tabClass("body")}
                    onClick={() => setActiveTab("body")}
                  >
                    Body
                  </button>
                  <button
                    className={tabClass("metadata")}
                    onClick={() => setActiveTab("metadata")}
                  >
                    Metadata
                    {metadataRows.length > 0 && (
                      <span className="ml-1 text-xs text-purple-500">
                        ({metadataRows.length})
                      </span>
                    )}
                  </button>
                </div>
                <div className="flex-grow overflow-hidden">
                  {activeTab === "body" ? (
                    <Editor
                      services={store.services}
                      data={body}
                      onChange={handleBodyChange}
                      currentMethod={{
                        service: activeRequest.service,
                        method: activeRequest.method,
                      }}
                    />
                  ) : (
                    <MetadataEditor
                      rows={metadataRows}
                      onChange={handleMetadataChange}
                    />
                  )}
                </div>
              </div>

              {/* Response */}
              <div className="w-1/2 flex flex-col">
                <ResponsePanel
                  response={response}
                  error={responseError}
                  loading={isInvoking}
                />
              </div>
            </div>
          </div>
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
