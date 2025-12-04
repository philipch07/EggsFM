import React, { useEffect, useRef, useState } from 'react';
import { SpeakerWaveIcon, SpeakerXMarkIcon } from '@heroicons/react/16/solid';

interface VolumeComponentProps {
  isMuted: boolean;
  onStateChanged: (isMuted: boolean) => void;
  onVolumeChanged: (value: number) => void;
}

const VolumeComponent = (props: VolumeComponentProps) => {
  const [isMuted, setIsMuted] = useState<boolean>(props.isMuted);
  const volumeRef = useRef<number>(10);

  useEffect(() => {
    props.onStateChanged(isMuted);
  }, [isMuted]);

  const onVolumeChange = (newValue: number) => {
    if (isMuted && newValue !== 0) {
      setIsMuted((_) => false);
    }
    if (!isMuted && newValue === 0) {
      setIsMuted((_) => true);
    }

    props.onVolumeChanged(newValue / 19);
  };
  return (
    <div className='field-row md:w-[300px]'>
      <label htmlFor='range22'>Volume:</label>
      <label htmlFor='range23'>Low</label>
      <input
        id='range23'
        type='range'
        min='0'
        max='19'
        defaultValue={volumeRef.current}
        onChange={(event) => onVolumeChange(parseInt(event.target.value))}
      />
      <label htmlFor='range24'>High</label>
    </div>
  );
};
export default VolumeComponent;
