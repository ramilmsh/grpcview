import React, { useState, useMemo } from "react";
import { X } from "lucide-react";
import { Service, Method } from "@grpcview/v1/workspace_pb";

interface RequestSelectorModalProps {
  show: boolean;
  services: Service[];
  onClose: () => void;
  onSelect: (service: Service, method: Method) => void;
}

export const RequestSelectorModal: React.FC<RequestSelectorModalProps> = ({
  show,
  services,
  onClose,
  onSelect,
}) => {
  const [selectedServiceIndex, setSelectedServiceIndex] = useState<
    number | null
  >(null);
  const [filter, setFilter] = useState("");

  const filteredServices = useMemo(() => {
    if (!filter) return services;
    const lower = filter.toLowerCase();
    return services.filter(
      (s) =>
        s.name.toLowerCase().includes(lower) ||
        s.package.toLowerCase().includes(lower) ||
        s.methods.some((m) => m.name.toLowerCase().includes(lower))
    );
  }, [services, filter]);

  if (!show) return null;

  return (
    <div className="fixed inset-0 bg-black/50 z-50 flex items-center justify-center">
      <div className="bg-white rounded shadow-lg w-[600px] h-[500px] flex flex-col overflow-hidden">
        <div className="flex justify-between items-center p-4 border-b">
          <h3 className="text-lg font-medium text-gray-900">Select Method</h3>
          <button
            onClick={onClose}
            className="text-gray-500 hover:text-gray-700"
          >
            <X size={20} />
          </button>
        </div>

        <div className="p-4 border-b">
          <input
            className="w-full border rounded px-3 py-2 outline-none focus:border-purple-600"
            placeholder="Search services or methods..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            autoFocus
          />
        </div>

        <div className="flex-grow flex overflow-hidden">
          {/* Services List */}
          <div className="w-1/2 border-r overflow-y-auto">
            {filteredServices.map((service, idx) => (
              <div
                key={`${service.package}.${service.name}`}
                className={`px-4 py-3 cursor-pointer hover:bg-gray-50 ${
                  selectedServiceIndex === idx
                    ? "bg-purple-50 border-l-4 border-purple-600"
                    : ""
                }`}
                onClick={() => setSelectedServiceIndex(idx)}
              >
                <div className="font-medium text-gray-900">{service.name}</div>
                <div className="text-xs text-gray-500">{service.package}</div>
              </div>
            ))}
          </div>

          {/* Methods List */}
          <div className="w-1/2 overflow-y-auto bg-gray-50">
            {selectedServiceIndex !== null &&
            filteredServices[selectedServiceIndex] ? (
              filteredServices[selectedServiceIndex].methods.map((method) => (
                <div
                  key={method.name}
                  className="px-4 py-3 cursor-pointer hover:bg-white hover:shadow-sm m-2 rounded transition-all"
                  onClick={() => {
                    onSelect(filteredServices[selectedServiceIndex!], method);
                    onClose();
                  }}
                >
                  <div className="font-medium text-purple-700">
                    {method.name}
                  </div>
                  <div className="text-xs text-gray-500 truncate mt-1">
                    Input: {method.input?.name}
                  </div>
                </div>
              ))
            ) : (
              <div className="flex items-center justify-center h-full text-gray-400 text-sm">
                Select a service
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};
