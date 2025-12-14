import React, { useRef } from "react";
import { X } from "lucide-react";

interface AddSourceModalProps {
  show: boolean;
  onClose: () => void;
  onAddReflection: (host: string, port: number) => void;
  onAddDescriptor: (file: File) => void;
}

export const AddSourceModal: React.FC<AddSourceModalProps> = ({
  show,
  onClose,
  onAddReflection,
  onAddDescriptor,
}) => {
  const hostRef = useRef<HTMLInputElement>(null);
  const portRef = useRef<HTMLInputElement>(null);

  if (!show) return null;

  const handleReflection = () => {
    const host = hostRef.current?.value || "127.0.0.1";
    const port = parseInt(portRef.current?.value || "0", 10);
    onAddReflection(host, port);
    onClose();
  };

  const handleDescriptor = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files[0]) {
      onAddDescriptor(e.target.files[0]);
      onClose();
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 z-50 flex items-center justify-center">
      <div className="bg-white rounded shadow-lg w-[500px] overflow-hidden">
        <div className="flex justify-between items-center p-4 border-b">
          <h3 className="text-lg font-medium text-gray-900">Add Source</h3>
          <button
            onClick={onClose}
            className="text-gray-500 hover:text-gray-700"
          >
            <X size={20} />
          </button>
        </div>

        <div className="p-4 space-y-6">
          {/* Reflection Section */}
          <div>
            <h4 className="font-medium text-purple-600 mb-2">
              Server Reflection
            </h4>
            <div className="flex gap-2">
              <input
                ref={hostRef}
                defaultValue="127.0.0.1"
                placeholder="Host"
                className="border rounded p-2 flex-grow outline-none focus:border-purple-600"
              />
              <input
                ref={portRef}
                placeholder="Port"
                type="number"
                className="border rounded p-2 w-24 outline-none focus:border-purple-600"
              />
              <button
                onClick={handleReflection}
                className="bg-purple-600 text-white px-4 py-2 rounded hover:bg-purple-700 uppercase text-sm font-medium"
              >
                Add
              </button>
            </div>
          </div>

          <div className="border-t"></div>

          {/* Descriptor Section */}
          <div>
            <h4 className="font-medium text-purple-600 mb-2">Descriptor Set</h4>
            <div className="flex items-center">
              <input
                type="file"
                className="block w-full text-sm text-gray-500 file:mr-4 file:py-2 file:px-4 file:rounded file:border-0 file:text-sm file:font-semibold file:bg-purple-50 file:text-purple-700 hover:file:bg-purple-100"
                onChange={handleDescriptor}
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
