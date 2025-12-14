import React, { useState } from "react";
import { ItemWithId } from "@/lib/store";
import {
  ChevronRight,
  ChevronDown,
  Folder,
  FolderOpen,
  FileText,
  FilePlus,
  FolderPlus,
  Check,
  X,
  Trash2,
} from "lucide-react";

interface TreeViewProps {
  item: ItemWithId;
  onAddItem: (parent: ItemWithId, item: ItemWithId) => void;
  onRemoveItem: (parent: ItemWithId, index: number) => void;
}

export const TreeView: React.FC<TreeViewProps> = ({
  item,
  onAddItem,
  onRemoveItem,
}) => {
  const [isOpen, setIsOpen] = useState(true);
  const [newType, setNewType] = useState<"grpc" | "folder" | null>(null);
  const [newName, setNewName] = useState("");

  const toggleOpen = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (item.type === "folder") {
      setIsOpen(!isOpen);
    }
  };

  const startAddFile = (e: React.MouseEvent) => {
    e.stopPropagation();
    setNewType("grpc");
    setNewName("");
    setIsOpen(true);
  };

  const startAddFolder = (e: React.MouseEvent) => {
    e.stopPropagation();
    setNewType("folder");
    setNewName("");
    setIsOpen(true);
  };

  const cancelAdd = () => {
    setNewType(null);
    setNewName("");
  };

  const confirmAdd = () => {
    if (newName.trim() && newType) {
      onAddItem(item, {
        name: newName,
        type: newType,
        id: crypto.randomUUID(),
        children: [],
      });
    }
    cancelAdd();
  };

  const handleRemoveChild = (child: ItemWithId, index: number) => {
    onRemoveItem(item, index);
  };

  return (
    <div className="font-roboto text-sm select-none text-gray-800">
      <div className="flex items-center px-2 h-8 cursor-pointer hover:bg-black/5 group rounded mx-1">
        <span
          className="flex items-center justifying-center w-6 h-6 mr-1 rounded-full hover:bg-black/10"
          onClick={toggleOpen}
          style={{ visibility: item.type === "folder" ? "visible" : "hidden" }}
        >
          {isOpen ? (
            <ChevronDown size={18} className="text-gray-500" />
          ) : (
            <ChevronRight size={18} className="text-gray-500" />
          )}
        </span>

        <span className="mr-2 text-gray-600" onClick={toggleOpen}>
          {item.type === "folder" ? (
            isOpen ? (
              <FolderOpen size={18} />
            ) : (
              <Folder size={18} />
            )
          ) : (
            <FileText size={18} />
          )}
        </span>

        <span
          className="flex-grow whitespace-nowrap overflow-hidden text-ellipsis font-normal text-gray-900"
          onClick={toggleOpen}
        >
          {item.name || "Root"}
        </span>

        <div className="flex items-center opacity-0 group-hover:opacity-100 transition-opacity">
          {item.type === "folder" && (
            <>
              <button
                onClick={startAddFile}
                title="Add Request"
                className="p-1 rounded-full hover:bg-black/10 text-gray-600"
              >
                <FilePlus size={16} />
              </button>
              <button
                onClick={startAddFolder}
                title="Add Group"
                className="p-1 rounded-full hover:bg-black/10 text-gray-600"
              >
                <FolderPlus size={16} />
              </button>
            </>
          )}
        </div>
      </div>

      {newType !== null && (
        <div className="flex items-center pl-8 pr-2 h-8">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") confirmAdd();
              if (e.key === "Escape") cancelAdd();
            }}
            placeholder="Name..."
            className="flex-grow border-none border-b-2 border-purple-600 bg-gray-100 text-sm px-2 py-1 outline-none rounded-t"
            autoFocus
          />
          <button
            onClick={confirmAdd}
            className="p-1 ml-1 rounded-full hover:bg-black/10 text-green-600"
          >
            <Check size={18} />
          </button>
          <button
            onClick={cancelAdd}
            className="p-1 ml-1 rounded-full hover:bg-black/10 text-red-600"
          >
            <X size={18} />
          </button>
        </div>
      )}

      {isOpen && item.children && item.children.length > 0 && (
        <div className="ml-7">
          {item.children.map((child, index) => (
            <div key={child.id || index} className="relative group/child">
              <TreeView
                item={child}
                onAddItem={onAddItem}
                onRemoveItem={onRemoveItem}
              />
              <button
                className="absolute top-1 right-2 opacity-0 group-hover/child:opacity-100 transition-opacity p-1 rounded-full hover:bg-black/10 text-gray-600 z-10"
                onClick={(e) => {
                  e.stopPropagation();
                  handleRemoveChild(child, index);
                }}
                title="Delete"
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};
