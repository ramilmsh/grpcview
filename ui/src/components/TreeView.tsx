import React, { useState, useRef, useEffect } from "react";
import { Item } from "@/lib/store";
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
  Edit2,
} from "lucide-react";

interface TreeViewProps {
  item: Item;
  onAddItem: (parent: Item, item: Item) => void;
  onRemoveItem: (parent: Item, index: number) => void;
  onRenameItem?: (item: Item, newName: string) => void;
  onSelect?: (item: Item) => void;
  onStartAddRequest?: (parent: Item) => void;
}

export const TreeView: React.FC<TreeViewProps> = ({
  item,
  onAddItem,
  onRemoveItem,
  onRenameItem,
  onSelect,
  onStartAddRequest,
}) => {
  const [isOpen, setIsOpen] = useState(true);
  const [isAddingFolder, setIsAddingFolder] = useState(false);
  const [isRenaming, setIsRenaming] = useState(false);
  const [newName, setNewName] = useState("");
  const renameInputRef = useRef<HTMLInputElement>(null);

  const isFolder = item.content.case === "folder";
  const children =
    item.content.case === "folder" ? item.content.value.items : [];

  useEffect(() => {
    if (isRenaming && renameInputRef.current) {
      renameInputRef.current.focus();
      setNewName(item.name);
    }
  }, [isRenaming, item.name]);

  const toggleOpen = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (isFolder) {
      setIsOpen(!isOpen);
    } else {
      onSelect?.(item);
    }
  };

  const startAddFolder = (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsAddingFolder(true);
    setNewName("");
    setIsOpen(true);
  };

  const handleAddRequest = (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsOpen(true);
    onStartAddRequest?.(item);
  };

  const startRename = (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsRenaming(true);
  };

  const cancelAdd = () => {
    setIsAddingFolder(false);
    setNewName("");
  };

  const cancelRename = () => {
    setIsRenaming(false);
    setNewName("");
  };

  const confirmAddFolder = () => {
    if (newName.trim()) {
      onAddItem(item, {
        name: newName,
        id: crypto.randomUUID(),
        content: { case: "folder", value: { items: [] } },
      });
    }
    cancelAdd();
  };

  const confirmRename = () => {
    if (newName.trim()) {
      onRenameItem?.(item, newName);
    }
    cancelRename();
  };

  const handleRemoveChild = (index: number) => {
    onRemoveItem(item, index);
  };

  return (
    <div className="font-roboto text-sm select-none text-gray-800">
      <div
        className="flex items-center px-2 h-8 cursor-pointer hover:bg-black/5 group rounded mx-1"
        onClick={toggleOpen}
      >
        <span
          className="flex items-center justifying-center w-6 h-6 mr-1 rounded-full hover:bg-black/10"
          style={{ visibility: isFolder ? "visible" : "hidden" }}
        >
          {isOpen ? (
            <ChevronDown size={18} className="text-gray-500" />
          ) : (
            <ChevronRight size={18} className="text-gray-500" />
          )}
        </span>

        <span className="mr-2 text-gray-600">
          {isFolder ? (
            isOpen ? (
              <FolderOpen size={18} />
            ) : (
              <Folder size={18} />
            )
          ) : (
            <FileText size={18} />
          )}
        </span>

        {isRenaming ? (
          <div
            className="flex-grow flex items-center"
            onClick={(e) => e.stopPropagation()}
          >
            <input
              ref={renameInputRef}
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") confirmRename();
                if (e.key === "Escape") cancelRename();
              }}
              className="flex-grow border-none border-b-2 border-purple-600 bg-gray-100 text-sm px-1 py-0.5 outline-none rounded-t"
            />
            <button
              onClick={(e) => {
                e.stopPropagation();
                confirmRename();
              }}
              className="p-1 ml-1 text-green-600 hover:bg-black/10 rounded-full"
            >
              <Check size={14} />
            </button>
            <button
              onClick={(e) => {
                e.stopPropagation();
                cancelRename();
              }}
              className="p-1 ml-1 text-red-600 hover:bg-black/10 rounded-full"
            >
              <X size={14} />
            </button>
          </div>
        ) : (
          <span className="flex-grow whitespace-nowrap overflow-hidden text-ellipsis font-normal text-gray-900">
            {item.name || "Root"}
          </span>
        )}

        {!isRenaming && (
          <div className="flex items-center opacity-0 group-hover:opacity-100 transition-opacity">
            {isFolder && (
              <>
                <button
                  onClick={handleAddRequest}
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
            <button
              onClick={startRename}
              title="Rename"
              className="p-1 rounded-full hover:bg-black/10 text-gray-600"
            >
              <Edit2 size={16} />
            </button>
          </div>
        )}
      </div>

      {isAddingFolder && (
        <div className="flex items-center pl-8 pr-2 h-8">
          <input
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") confirmAddFolder();
              if (e.key === "Escape") cancelAdd();
            }}
            placeholder="Folder Name..."
            className="flex-grow border-none border-b-2 border-purple-600 bg-gray-100 text-sm px-2 py-1 outline-none rounded-t"
            autoFocus
          />
          <button
            onClick={confirmAddFolder}
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

      {isOpen && children.length > 0 && (
        <div className="ml-7">
          {children.map((child, index) => (
            <div key={child.id || index} className="relative group/child">
              <TreeView
                item={child}
                onAddItem={onAddItem}
                onRemoveItem={onRemoveItem}
                onRenameItem={onRenameItem}
                onSelect={onSelect}
                onStartAddRequest={onStartAddRequest}
              />
              <button
                className="absolute top-1 right-2 opacity-0 group-hover/child:opacity-100 transition-opacity p-1 rounded-full hover:bg-black/10 text-gray-600 z-10"
                onClick={(e) => {
                  e.stopPropagation();
                  handleRemoveChild(index);
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
